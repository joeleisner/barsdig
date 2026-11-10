package ogg

import (
	"bytes"
	"testing"

	"barsdig/internal/bwav"
)

// FuzzWriteOpus feeds arbitrary packets to the muxer; it may return errors but must not panic.
func FuzzWriteOpus(f *testing.F) {
	f.Add([]byte{0xF8, 0xFF, 0xFE}, []byte{0xF8, 0x01}, uint16(120), 100)
	f.Add([]byte{0xFB}, []byte{0x00}, uint16(0), 0)
	f.Fuzz(func(t *testing.T, a, b []byte, skip uint16, samples int) {
		s := []bwav.OpusStream{
			{PreSkip: skip, Samples: samples, Packets: [][]byte{a, b}},
			{PreSkip: skip, Samples: samples, Packets: [][]byte{b, a}},
		}
		_ = WriteOpus(&bytes.Buffer{}, s)
		_ = WriteOpus(&bytes.Buffer{}, s[:1])
		_, _ = PacketSamples(a)
	})
}
