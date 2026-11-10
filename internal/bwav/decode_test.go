package bwav

import (
	"errors"
	"reflect"
	"testing"

	"barsdig/internal/synth"
)

// One DSP frame: header byte (pair index<<4 | scale), then 7 bytes of nibbles (high nibble first).
func dsp(spec synth.BWAVSpec, channels ...[]byte) []byte {
	spec.Codec = CodecDSP
	spec.Channels = channels
	return synth.BWAV(spec)
}

func TestDSPGoldenZeroCoefs(t *testing.T) {
	// zero coefficients, scale 0: output == sign-extended nibble.
	frame := []byte{0x00, 0x12, 0x3F, 0x07, 0x80, 0x00, 0x00, 0x00}
	p, err := Decode(dsp(synth.BWAVSpec{SampleRate: 48000, FullSamples: 14, Samples: 14}, frame))
	if err != nil {
		t.Fatal(err)
	}
	want := []int16{1, 2, 3, -1, 0, 7, -8, 0, 0, 0, 0, 0, 0, 0}
	if p.Channels != 1 || p.SampleRate != 48000 || !reflect.DeepEqual(p.Samples[0], want) {
		t.Fatalf("got %v (ch %d rate %d), want %v", p.Samples[0], p.Channels, p.SampleRate, want)
	}
}

func TestDSPScaleShiftsNibble(t *testing.T) {
	frame := []byte{0x02, 0x12, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00} // scale 2 -> x4
	p, err := Decode(dsp(synth.BWAVSpec{SampleRate: 1, FullSamples: 2, Samples: 2}, frame))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Samples[0], []int16{4, 8}) {
		t.Fatalf("got %v", p.Samples[0])
	}
}

func TestDSPHistoryCarriesAcrossFrames(t *testing.T) {
	// coef pair 0 = (2048, 0): out = nibble + hist1, so samples are running sums.
	spec := synth.BWAVSpec{SampleRate: 1, FullSamples: 16, Samples: 16, Coefs: [][16]int16{{2048, 0}}}
	f1 := []byte{0x00, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11} // fourteen 1s
	f2 := []byte{0x00, 0x11, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00} // 1, 1, then zeros
	p, err := Decode(dsp(spec, append(f1, f2...)))
	if err != nil {
		t.Fatal(err)
	}
	want := []int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	if !reflect.DeepEqual(p.Samples[0], want) {
		t.Fatalf("got %v", p.Samples[0])
	}
}

func TestDSPInitialHistory(t *testing.T) {
	spec := synth.BWAVSpec{SampleRate: 1, FullSamples: 1, Samples: 1, Coefs: [][16]int16{{2048, 0}}, Hist: [][3]int16{{0, 100, 0}}}
	p, err := Decode(dsp(spec, []byte{0x00, 0x10, 0, 0, 0, 0, 0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Samples[0][0] != 101 {
		t.Fatalf("got %d, want 101", p.Samples[0][0])
	}
}

func TestDSPClampsTo16Bit(t *testing.T) {
	spec := synth.BWAVSpec{SampleRate: 1, FullSamples: 3, Samples: 3, Coefs: [][16]int16{{0, 0}}}
	p, err := Decode(dsp(spec, []byte{0x0F, 0x77, 0x70, 0, 0, 0, 0, 0})) // scale 15: 7<<15 overflows
	if err != nil {
		t.Fatal(err)
	}
	if p.Samples[0][0] != 32767 || p.Samples[0][1] != 32767 {
		t.Fatalf("got %v", p.Samples[0][:3])
	}
}

func TestPCM16Roundtrip(t *testing.T) {
	raw := []byte{0x01, 0x00, 0xFF, 0xFF, 0x00, 0x80, 0xFF, 0x7F}
	d := synth.BWAV(synth.BWAVSpec{Codec: CodecPCM16, SampleRate: 22050, FullSamples: 4, Samples: 4, Channels: [][]byte{raw}})
	p, err := Decode(d)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Samples[0], []int16{1, -1, -32768, 32767}) || p.SampleRate != 22050 {
		t.Fatalf("got %v rate %d", p.Samples[0], p.SampleRate)
	}
}

func TestStereoSplitsChannels(t *testing.T) {
	l := []byte{0x00, 0x11, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	r := []byte{0x00, 0x22, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	p, err := Decode(dsp(synth.BWAVSpec{SampleRate: 1, FullSamples: 2, Samples: 2}, l, r))
	if err != nil {
		t.Fatal(err)
	}
	if p.Channels != 2 || p.Samples[0][0] != 1 || p.Samples[1][0] != 2 || len(p.Pan) != 2 || p.Pan[1] != 1 {
		t.Fatalf("got %+v", p)
	}
}

func TestLoopCarriedThrough(t *testing.T) {
	loop := [2]int32{3, 14}
	frame := make([]byte, 8)
	p, err := Decode(dsp(synth.BWAVSpec{SampleRate: 1, FullSamples: 14, Samples: 14, Loop: &loop}, frame))
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasLoop || p.LoopStart != 3 || p.LoopEnd != 14 {
		t.Fatalf("loop = %v %d %d", p.HasLoop, p.LoopStart, p.LoopEnd)
	}
	q, _ := Decode(dsp(synth.BWAVSpec{SampleRate: 1, FullSamples: 14, Samples: 14}, frame))
	if q.HasLoop {
		t.Fatal("unexpected loop")
	}
}

func TestOpusReturnsUnsupported(t *testing.T) {
	d := synth.BWAV(synth.BWAVSpec{Codec: CodecOpus, SampleRate: 48000, FullSamples: 5, Samples: 5, Channels: [][]byte{make([]byte, 8)}})
	_, err := Decode(d)
	var ue *UnsupportedCodecError
	if !errors.As(err, &ue) || ue.Codec != CodecOpus {
		t.Fatalf("err = %v", err)
	}
}

func TestTruncatedDataErrors(t *testing.T) {
	d := dsp(synth.BWAVSpec{SampleRate: 1, FullSamples: 28, Samples: 28}, make([]byte, 8)) // needs 16 bytes of data
	d = d[:len(d)-0x40+8]                                                                  // cut the channel data short
	if _, err := Decode(d); err == nil {
		t.Fatal("expected error for truncated channel data")
	}
	huge := dsp(synth.BWAVSpec{SampleRate: 1, FullSamples: 0xFFFFFFF0, Samples: 0xFFFFFFF0}, make([]byte, 8))
	if _, err := Decode(huge); err == nil {
		t.Fatal("expected error for absurd sample count (no giant allocation)")
	}
}

// A DSP stream may end in a partial frame: 16 samples = one full frame (14)
// plus a frame header byte and one nibble byte for the remaining 2. Real files
// end exactly there, with no padding to a full 8-byte frame.
func TestDSPPartialFinalFrame(t *testing.T) {
	data := append([]byte{0x00, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11}, 0x00, 0x21)
	spec := synth.BWAVSpec{Codec: CodecDSP, SampleRate: 1, FullSamples: 16, Samples: 16, Coefs: [][16]int16{{2048, 0}}, Channels: [][]byte{data}}
	file := synth.BWAV(spec)
	info, _ := ParseInfo(file)
	end := int(info.Channels[0].DataOffset) + len(data)
	p, err := Decode(file[:end]) // file ends right after the partial frame
	if err != nil {
		t.Fatal(err)
	}
	want := []int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 16, 17} // last two nibbles are 2 and 1
	if !reflect.DeepEqual(p.Samples[0], want) {
		t.Fatalf("got %v", p.Samples[0])
	}
	if _, err := Decode(file[:end-1]); err == nil { // the last nibble byte missing
		t.Fatal("expected error when the final frame's nibble bytes are missing")
	}
}
