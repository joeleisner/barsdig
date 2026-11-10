// Package wav writes 16-bit PCM WAV files, with an optional smpl loop chunk.
package wav

import (
	"encoding/binary"
	"fmt"
	"io"

	"barsdig/internal/bwav"
)

// channelMask returns the speaker mask used for more than two channels. The
// standard Microsoft ordering is assumed (FL FR FC LFE BL BR for 5.1); BWAV
// pan ids are not mapped because their meaning beyond L/R/C is unverified.
func channelMask(n int) uint32 {
	switch n {
	case 3:
		return 0x7 // FL FR FC
	case 4:
		return 0x33 // FL FR BL BR
	case 5:
		return 0x37 // FL FR FC BL BR
	case 6:
		return 0x3F // FL FR FC LFE BL BR
	}
	return 0
}

func fmtLen(p *bwav.PCM) int {
	if p.Channels > 2 {
		return 40
	}
	return 16
}

const smplLen = 60

// Size is the exact number of bytes Write produces for p.
func Size(p *bwav.PCM) int {
	n := 12 + 8 + fmtLen(p) + 8 + 2*p.Channels*len(p.Samples[0])
	if p.HasLoop {
		n += 8 + smplLen
	}
	return n
}

// Encode returns p as an interleaved 16-bit PCM WAV file (exactly Size(p) bytes). p must be consistent
// (len(p.Samples) == p.Channels, equal channel lengths); Write checks that. When p has a loop, a
// smpl chunk carries it; BWAV loop ends are exclusive and smpl ends are
// inclusive, so the written end is LoopEnd-1.
func Encode(p *bwav.PCM) []byte {
	frames := len(p.Samples[0])
	buf := make([]byte, 0, Size(p))
	u16 := func(v uint16) { buf = binary.LittleEndian.AppendUint16(buf, v) }
	u32 := func(v uint32) { buf = binary.LittleEndian.AppendUint32(buf, v) }

	buf = append(buf, "RIFF"...)
	u32(uint32(Size(p) - 8))
	buf = append(buf, "WAVEfmt "...)
	u32(uint32(fmtLen(p)))
	blockAlign := uint16(2 * p.Channels)
	if p.Channels > 2 {
		u16(0xFFFE)
	} else {
		u16(1)
	}
	u16(uint16(p.Channels))
	u32(uint32(p.SampleRate))
	u32(uint32(p.SampleRate) * uint32(blockAlign))
	u16(blockAlign)
	u16(16)
	if p.Channels > 2 {
		u16(22) // cbSize
		u16(16) // valid bits
		u32(channelMask(p.Channels))
		// KSDATAFORMAT_SUBTYPE_PCM {00000001-0000-0010-8000-00AA00389B71}
		buf = append(buf, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71)
	}
	buf = append(buf, "data"...)
	u32(uint32(2 * p.Channels * frames))
	for i := 0; i < frames; i++ {
		for ch := 0; ch < p.Channels; ch++ {
			u16(uint16(p.Samples[ch][i]))
		}
	}
	if p.HasLoop {
		buf = append(buf, "smpl"...)
		u32(smplLen)
		rate := uint32(p.SampleRate)
		period := uint32(0)
		if rate > 0 {
			period = 1000000000 / rate
		}
		u32(0)      // manufacturer
		u32(0)      // product
		u32(period) // sample period (ns)
		u32(60)     // MIDI unity note
		u32(0)      // pitch fraction
		u32(0)      // SMPTE format
		u32(0)      // SMPTE offset
		u32(1)      // number of loops
		u32(0)      // sampler data
		u32(0)      // cue point id
		u32(0)      // forward loop
		u32(uint32(p.LoopStart))
		end := p.LoopEnd - 1
		if end < p.LoopStart {
			end = p.LoopStart
		}
		u32(uint32(end))
		u32(0) // fraction
		u32(0) // play count (infinite)
	}
	return buf
}

// Write writes p as WAV (see Encode), rejecting inconsistent input.
func Write(w io.Writer, p *bwav.PCM) error {
	if p.Channels < 1 || len(p.Samples) != p.Channels {
		return fmt.Errorf("wav: %d channels but %d sample slices", p.Channels, len(p.Samples))
	}
	for _, s := range p.Samples {
		if len(s) != len(p.Samples[0]) {
			return fmt.Errorf("wav: channels have different lengths")
		}
	}
	_, err := w.Write(Encode(p))
	return err
}
