package bwav

import "fmt"

// PCM is decoded audio, one slice of 16-bit samples per channel.
type PCM struct {
	SampleRate, Channels int
	Samples              [][]int16
	Pan                  []uint16
	HasLoop              bool
	LoopStart, LoopEnd   int
}

// UnsupportedCodecError is returned by Decode for codecs it cannot decode
// (Opus, or an unknown id).
type UnsupportedCodecError struct{ Codec uint16 }

func (e *UnsupportedCodecError) Error() string {
	return fmt.Sprintf("bwav: unsupported codec %d", e.Codec)
}

// Decode decodes a PCM16 or DSP-ADPCM BWAV file.
func Decode(data []byte) (*PCM, error) {
	info, err := ParseInfo(data)
	if err != nil {
		return nil, err
	}
	codec := info.Channels[0].Codec
	for _, c := range info.Channels {
		if c.Codec != codec {
			return nil, fmt.Errorf("bwav: mixed codecs %d and %d", codec, c.Codec)
		}
	}
	if codec != CodecPCM16 && codec != CodecDSP {
		return nil, &UnsupportedCodecError{Codec: codec}
	}
	p := &PCM{
		SampleRate: int(info.Channels[0].SampleRate),
		Channels:   len(info.Channels),
		Samples:    make([][]int16, len(info.Channels)),
		Pan:        make([]uint16, len(info.Channels)),
	}
	p.LoopStart, p.LoopEnd, p.HasLoop = info.Loop()
	for i, c := range info.Channels {
		p.Pan[i] = c.Pan
		var s []int16
		if codec == CodecPCM16 {
			s, err = decodePCM16(data, c)
		} else {
			s, err = decodeDSP(data, c)
		}
		if err != nil {
			return nil, fmt.Errorf("bwav: channel %d: %w", i, err)
		}
		p.Samples[i] = s
	}
	return p, nil
}

func chanData(data []byte, off uint32, need uint64) ([]byte, error) {
	if uint64(off)+need > uint64(len(data)) {
		return nil, fmt.Errorf("data (%d bytes at %#x) runs past end of file (%d)", need, off, len(data))
	}
	return data[off : uint64(off)+need], nil
}

func decodePCM16(data []byte, c ChannelInfo) ([]int16, error) {
	raw, err := chanData(data, c.DataOffset, 2*uint64(c.Samples))
	if err != nil {
		return nil, err
	}
	out := make([]int16, c.Samples)
	for i := range out {
		out[i] = int16(le.Uint16(raw[2*i:]))
	}
	return out, nil
}

func clamp16(v int32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func decodeDSP(data []byte, c ChannelInfo) ([]int16, error) {
	frames := (uint64(c.Samples) + 13) / 14
	// The last frame may be partial: a header byte plus one byte per two samples.
	need := uint64(c.Samples) / 14 * 8
	if rem := uint64(c.Samples) % 14; rem > 0 {
		need += 1 + (rem+1)/2
	}
	raw, err := chanData(data, c.DataOffset, need)
	if err != nil {
		return nil, err
	}
	out := make([]int16, c.Samples)
	h1, h2 := int32(c.Hist1), int32(c.Hist2)
	n := 0
	for f := uint64(0); f < frames; f++ {
		fr := raw[f*8 : min(f*8+8, uint64(len(raw)))]
		scale := int32(1) << (fr[0] & 0xF)
		pair := int(fr[0]>>4) & 7
		c1, c2 := int32(c.Coefs[2*pair]), int32(c.Coefs[2*pair+1])
		for k := 0; k < 14 && n < len(out); k++ {
			b := fr[1+k/2]
			nib := int32(b >> 4)
			if k%2 == 1 {
				nib = int32(b & 0xF)
			}
			if nib >= 8 {
				nib -= 16
			}
			s := clamp16(((nib*scale)<<11 + 1024 + c1*h1 + c2*h2) >> 11)
			out[n] = s
			n++
			h2, h1 = h1, int32(s)
		}
	}
	return out, nil
}
