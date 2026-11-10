package bwav

import (
	"testing"

	"barsdig/internal/synth"
)

// FuzzBWAV feeds arbitrary bytes to every BWAV entry point; none may panic.
func FuzzBWAV(f *testing.F) {
	f.Add(synth.BWAV(synth.BWAVSpec{Codec: CodecDSP, SampleRate: 48000, FullSamples: 28, Samples: 28, Coefs: [][16]int16{{2048, 0}},
		Channels: [][]byte{make([]byte, 16)}}))
	f.Add(synth.BWAV(synth.BWAVSpec{Codec: CodecPCM16, SampleRate: 8000, FullSamples: 4, Samples: 4, Channels: [][]byte{make([]byte, 8), make([]byte, 8)}}))
	f.Add(opusBWAV([][]byte{{0xF8, 1, 2}, {}}, [][]byte{{0xF8, 3}, {0xF8, 4}}))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseInfo(data)
		_, _ = Decode(data)
		_, _ = OpusStreams(data)
	})
}
