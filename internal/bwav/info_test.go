package bwav

import (
	"encoding/binary"
	"testing"

	"barsdig/internal/synth"
)

func TestParseInfoRoundTrip(t *testing.T) {
	loop := [2]int32{100, 900}
	d := synth.BWAV(synth.BWAVSpec{
		Codec: CodecDSP, SampleRate: 32000, FullSamples: 1000, Samples: 1000, Loop: &loop,
		Coefs:    [][16]int16{{1, -2, 3}, {4, 5, 6}},
		Hist:     [][3]int16{{7, 8, 9}, {10, 11, 12}},
		Channels: [][]byte{make([]byte, 16), make([]byte, 16)},
	})
	i, err := ParseInfo(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(i.Channels) != 2 || i.Prefetch || i.Version != 1 {
		t.Fatalf("info = %+v", i)
	}
	c := i.Channels[1]
	if c.Codec != CodecDSP || c.Pan != 1 || c.SampleRate != 32000 || c.FullSamples != 1000 || c.Samples != 1000 {
		t.Errorf("channel 1 = %+v", c)
	}
	if c.Coefs[0] != 4 || c.Coefs[2] != 6 || c.Predictor != 10 || c.Hist1 != 11 || c.Hist2 != 12 {
		t.Errorf("channel 1 dsp state = %+v", c)
	}
	if i.Channels[0].Coefs[1] != -2 {
		t.Error("negative coef lost")
	}
	if c.DataOffset%0x40 != 0 || int(c.DataOffset) >= len(d) {
		t.Errorf("data offset %#x", c.DataOffset)
	}
	if i.HeaderLen() != 0x10+2*0x4C {
		t.Errorf("HeaderLen = %d", i.HeaderLen())
	}
	if i.CodecName() != "dsp-adpcm" {
		t.Errorf("CodecName = %q", i.CodecName())
	}
}

func TestSixChannelOpusCoded(t *testing.T) {
	chans := make([][]byte, 6)
	for k := range chans {
		chans[k] = make([]byte, 8)
	}
	i, err := ParseInfo(synth.BWAV(synth.BWAVSpec{Codec: CodecOpus, SampleRate: 48000, FullSamples: 5, Samples: 5, Channels: chans}))
	if err != nil {
		t.Fatal(err)
	}
	if len(i.Channels) != 6 || i.CodecName() != "opus" || i.Channels[5].Pan != 5 {
		t.Fatalf("info = %+v", i)
	}
}

func TestUnknownCodecName(t *testing.T) {
	i, err := ParseInfo(synth.BWAV(synth.BWAVSpec{Codec: 9, SampleRate: 1, Samples: 1, FullSamples: 1, Channels: [][]byte{{0}}}))
	if err != nil {
		t.Fatal(err)
	}
	if i.CodecName() != "unknown:9" {
		t.Errorf("CodecName = %q", i.CodecName())
	}
}

func TestPrefetchFlag(t *testing.T) {
	d := synth.BWAV(synth.BWAVSpec{Codec: CodecDSP, SampleRate: 48000, FullSamples: 735894, Samples: 14336, Prefetch: true, Channels: [][]byte{make([]byte, 8)}})
	i, err := ParseInfo(d)
	if err != nil {
		t.Fatal(err)
	}
	if !i.Prefetch || i.Channels[0].Samples != 14336 || i.Channels[0].FullSamples != 735894 {
		t.Fatalf("info = %+v", i)
	}
}

func TestLoopNoneAndPoints(t *testing.T) {
	none, _ := ParseInfo(synth.BWAV(synth.BWAVSpec{Codec: CodecDSP, SampleRate: 1, Samples: 1, FullSamples: 1, Channels: [][]byte{{0}}}))
	if _, _, ok := none.Loop(); ok {
		t.Error("loop reported for LoopEnd -1")
	}
	loop := [2]int32{10, 20}
	with, _ := ParseInfo(synth.BWAV(synth.BWAVSpec{Codec: CodecDSP, SampleRate: 1, Samples: 30, FullSamples: 30, Loop: &loop, Channels: [][]byte{{0}}}))
	if s, e, ok := with.Loop(); !ok || s != 10 || e != 20 {
		t.Errorf("Loop() = %d,%d,%v", s, e, ok)
	}
}

func TestBadMagic(t *testing.T) {
	d := synth.BWAV(synth.BWAVSpec{Codec: CodecDSP, SampleRate: 1, Samples: 1, FullSamples: 1, Channels: [][]byte{{0}}})
	d[0] = 'X'
	if _, err := ParseInfo(d); err == nil {
		t.Fatal("bad magic accepted")
	}
}

func TestTruncatedHeaderErrors(t *testing.T) {
	d := synth.BWAV(synth.BWAVSpec{Codec: CodecDSP, SampleRate: 1, Samples: 1, FullSamples: 1, Channels: [][]byte{{0}, {0}}})
	hl := 0x10 + 2*0x4C
	for n := 0; n < hl; n++ {
		if _, err := ParseInfo(d[:n]); err == nil {
			t.Fatalf("prefix %d accepted", n)
		}
	}
	if _, err := ParseInfo(d[:hl]); err != nil {
		t.Fatalf("header-only prefix rejected: %v", err)
	}
}

func TestChannelCountZeroOrHugeRejected(t *testing.T) {
	d := synth.BWAV(synth.BWAVSpec{Codec: CodecDSP, SampleRate: 1, Samples: 1, FullSamples: 1, Channels: [][]byte{{0}}})
	for _, n := range []uint16{0, 0xFFFF, 33} {
		binary.LittleEndian.PutUint16(d[0x0E:], n)
		if _, err := ParseInfo(d); err == nil {
			t.Errorf("channel count %d accepted", n)
		}
	}
}
