// Package bwav reads BWAV audio files: header parsing, PCM16 and
// DSP-ADPCM decoding, and Opus stream extraction.
package bwav

import (
	"encoding/binary"
	"fmt"
)

// Codec ids stored in each channel header.
const (
	CodecPCM16 = 0
	CodecDSP   = 1
	CodecOpus  = 2
)

const maxChannels = 32

var le = binary.LittleEndian

// ChannelInfo is the per-channel header (0x4C bytes).
type ChannelInfo struct {
	Codec, Pan              uint16
	SampleRate              uint32
	FullSamples, Samples    uint32 // full sound / this file (differ for a prefetch stub)
	Coefs                   [16]int16
	DataOffset              uint32
	LoopEnd, LoopStart      int32 // LoopEnd -1 means no loop
	Predictor, Hist1, Hist2 int16
}

// Info is the parsed BWAV header.
type Info struct {
	Version  uint16
	Prefetch bool
	Channels []ChannelInfo
}

// ParseInfo parses the BWAV header. It needs only the header prefix of the file.
func ParseInfo(data []byte) (*Info, error) {
	if len(data) < 0x10 {
		return nil, fmt.Errorf("bwav: header too short (%d bytes)", len(data))
	}
	if string(data[:4]) != "BWAV" {
		return nil, fmt.Errorf("bwav: bad magic %q", data[:4])
	}
	n := int(le.Uint16(data[0x0E:]))
	if n < 1 || n > maxChannels {
		return nil, fmt.Errorf("bwav: unreasonable channel count %d", n)
	}
	if len(data) < 0x10+0x4C*n {
		return nil, fmt.Errorf("bwav: header truncated: %d channels need %d bytes, have %d", n, 0x10+0x4C*n, len(data))
	}
	info := &Info{Version: le.Uint16(data[6:]), Prefetch: le.Uint16(data[0x0C:]) == 1, Channels: make([]ChannelInfo, n)}
	for i := range info.Channels {
		ch := data[0x10+0x4C*i:]
		c := &info.Channels[i]
		c.Codec = le.Uint16(ch[0:])
		c.Pan = le.Uint16(ch[2:])
		c.SampleRate = le.Uint32(ch[4:])
		c.FullSamples = le.Uint32(ch[8:])
		c.Samples = le.Uint32(ch[0xC:])
		for k := range c.Coefs {
			c.Coefs[k] = int16(le.Uint16(ch[0x10+2*k:]))
		}
		c.DataOffset = le.Uint32(ch[0x34:])
		c.LoopEnd = int32(le.Uint32(ch[0x3C:]))
		c.LoopStart = int32(le.Uint32(ch[0x40:]))
		c.Predictor = int16(le.Uint16(ch[0x44:]))
		c.Hist1 = int16(le.Uint16(ch[0x46:]))
		c.Hist2 = int16(le.Uint16(ch[0x48:]))
	}
	return info, nil
}

// HeaderLen is the byte length of the header: 0x10 plus 0x4C per channel.
func (i *Info) HeaderLen() int { return 0x10 + 0x4C*len(i.Channels) }

// Loop reports the loop points of the first channel; ok is false when the
// sound does not loop.
func (i *Info) Loop() (start, end int, ok bool) {
	c := i.Channels[0]
	if c.LoopEnd == -1 {
		return 0, 0, false
	}
	return int(c.LoopStart), int(c.LoopEnd), true
}

// CodecName names the codec of the first channel.
func (i *Info) CodecName() string {
	switch c := i.Channels[0].Codec; c {
	case CodecPCM16:
		return "pcm16"
	case CodecDSP:
		return "dsp-adpcm"
	case CodecOpus:
		return "opus"
	default:
		return fmt.Sprintf("unknown:%d", c)
	}
}
