package bwav

import (
	"encoding/binary"
	"fmt"
)

// OpusStream is the Opus data of one BWAV channel: an independent mono
// stream of plain Opus packets (docs/opus.md).
type OpusStream struct {
	Channel int      // BWAV channel index
	PreSkip uint16   // decoder pre-skip in 48 kHz samples
	Samples int      // BWAV per-channel sample count (true length)
	Packets [][]byte // zero-length packets are replaced by an Opus silence packet
}

var opusSilence = []byte{0xF8, 0xFF, 0xFE}

// OpusStreams extracts one Opus stream per channel from an Opus-coded BWAV.
// Any other codec yields *UnsupportedCodecError.
func OpusStreams(data []byte) ([]OpusStream, error) {
	info, err := ParseInfo(data)
	if err != nil {
		return nil, err
	}
	for _, c := range info.Channels {
		if c.Codec != CodecOpus {
			return nil, &UnsupportedCodecError{Codec: c.Codec}
		}
	}
	out := make([]OpusStream, len(info.Channels))
	for i, c := range info.Channels {
		s, err := nxOpus(data, c)
		if err != nil {
			return nil, fmt.Errorf("bwav: channel %d: %w", i, err)
		}
		s.Channel = i
		out[i] = s
	}
	return out, nil
}

func nxOpus(data []byte, c ChannelInfo) (OpusStream, error) {
	off := uint64(c.DataOffset)
	if off+0x20 > uint64(len(data)) {
		return OpusStream{}, fmt.Errorf("Opus stream at %#x runs past end of file", off)
	}
	if le.Uint32(data[off:]) != 0x80000001 {
		return OpusStream{}, fmt.Errorf("no NX Opus header at %#x", off)
	}
	dOff := off + uint64(le.Uint32(data[off+0x10:]))
	if dOff+8 > uint64(len(data)) || le.Uint32(data[dOff:]) != 0x80000004 {
		return OpusStream{}, fmt.Errorf("no NX Opus data chunk at %#x", dOff)
	}
	size := uint64(le.Uint32(data[dOff+4:]))
	p, end := dOff+8, dOff+8+size
	if end > uint64(len(data)) {
		return OpusStream{}, fmt.Errorf("Opus data (%d bytes) runs past end of file", size)
	}
	s := OpusStream{PreSkip: le.Uint16(data[off+0x1C:]), Samples: int(c.Samples)}
	for p < end {
		if p+8 > end {
			return OpusStream{}, fmt.Errorf("truncated packet header at %#x", p)
		}
		n := uint64(binary.BigEndian.Uint32(data[p:]))
		p += 8 // size + final range
		if p+n > end {
			return OpusStream{}, fmt.Errorf("packet of %d bytes at %#x runs past the data chunk", n, p)
		}
		pkt := data[p : p+n]
		if n == 0 {
			pkt = opusSilence
		}
		s.Packets = append(s.Packets, pkt)
		p += n
	}
	if len(s.Packets) == 0 {
		return OpusStream{}, fmt.Errorf("no Opus packets")
	}
	return s, nil
}
