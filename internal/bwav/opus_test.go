package bwav

import (
	"bytes"
	"errors"
	"testing"

	"barsdig/internal/synth"
)

var silence = []byte{0xF8, 0xFF, 0xFE}

func opusBWAV(streams ...[][]byte) []byte {
	chans := make([][]byte, len(streams))
	for i, p := range streams {
		chans[i] = synth.NXOpus(p, 120)
	}
	return synth.BWAV(synth.BWAVSpec{Codec: CodecOpus, SampleRate: 48000, FullSamples: 2000, Samples: 2000, Channels: chans})
}

func TestOpusStreamsFromSpikeLayout(t *testing.T) {
	a := [][]byte{{0xF8, 1, 2, 3, 4}, {0xF8, 9}}
	b := [][]byte{{0xF8, 5}, {0xF8, 6, 7}}
	st, err := OpusStreams(opusBWAV(a, b))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 {
		t.Fatalf("%d streams", len(st))
	}
	for i, want := range [][][]byte{a, b} {
		s := st[i]
		if s.Channel != i || s.PreSkip != 120 || s.Samples != 2000 || len(s.Packets) != 2 {
			t.Fatalf("stream %d = %+v", i, s)
		}
		for k := range want {
			if !bytes.Equal(s.Packets[k], want[k]) {
				t.Errorf("stream %d packet %d = % x, want % x", i, k, s.Packets[k], want[k])
			}
		}
	}
}

func TestEmptyPacketBecomesSilence(t *testing.T) {
	st, err := OpusStreams(opusBWAV([][]byte{{0xF8, 1}, {}, {0xF8, 2}}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(st[0].Packets[1], silence) {
		t.Fatalf("packet 1 = % x, want % x", st[0].Packets[1], silence)
	}
}

func TestNonOpusErrors(t *testing.T) {
	d := synth.BWAV(synth.BWAVSpec{Codec: CodecDSP, SampleRate: 1, Samples: 1, FullSamples: 1, Channels: [][]byte{make([]byte, 8)}})
	var ue *UnsupportedCodecError
	if _, err := OpusStreams(d); !errors.As(err, &ue) || ue.Codec != CodecDSP {
		t.Fatalf("err = %v", err)
	}
}

func TestTruncatedOpusNeverPanics(t *testing.T) {
	d := opusBWAV([][]byte{{0xF8, 1, 2, 3}, {0xF8, 4}}, [][]byte{{0xF8, 5}, {0xF8, 6}})
	for n := 0; n <= len(d); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on prefix %d: %v", n, r)
				}
			}()
			_, _ = OpusStreams(d[:n])
		}()
	}
}

func TestOpusBadChunkMagic(t *testing.T) {
	d := opusBWAV([][]byte{{0xF8, 1}})
	info, _ := ParseInfo(d)
	d[info.Channels[0].DataOffset] ^= 0xFF
	if _, err := OpusStreams(d); err == nil {
		t.Fatal("bad NX Opus magic accepted")
	}
}
