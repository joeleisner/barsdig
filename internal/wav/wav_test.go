package wav

import (
	"bytes"
	"encoding/binary"
	"testing"

	"barsdig/internal/bwav"
)

var le = binary.LittleEndian

func chunks(t *testing.T, w []byte) map[string][]byte {
	t.Helper()
	if string(w[0:4]) != "RIFF" || string(w[8:12]) != "WAVE" || int(le.Uint32(w[4:]))+8 != len(w) {
		t.Fatalf("bad RIFF header: % x (len %d)", w[:12], len(w))
	}
	m := map[string][]byte{}
	for p := 12; p < len(w); {
		sz := int(le.Uint32(w[p+4:]))
		m[string(w[p:p+4])] = w[p+8 : p+8+sz]
		p += 8 + sz + sz%2
	}
	return m
}

func render(t *testing.T, p *bwav.PCM) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := Write(&b, p); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func mono() *bwav.PCM {
	return &bwav.PCM{SampleRate: 44100, Channels: 1, Samples: [][]int16{{1, -2, 3}}, Pan: []uint16{0}}
}

func TestHeaderFields(t *testing.T) {
	c := chunks(t, render(t, mono()))
	f := c["fmt "]
	if le.Uint16(f[0:]) != 1 || le.Uint16(f[2:]) != 1 || le.Uint32(f[4:]) != 44100 ||
		le.Uint32(f[8:]) != 88200 || le.Uint16(f[12:]) != 2 || le.Uint16(f[14:]) != 16 {
		t.Fatalf("fmt = % x", f)
	}
	if len(c["data"]) != 6 {
		t.Fatalf("data len %d", len(c["data"]))
	}
}

func TestInterleaving(t *testing.T) {
	p := &bwav.PCM{SampleRate: 8000, Channels: 2, Samples: [][]int16{{1, 2, 3}, {-1, -2, -3}}, Pan: []uint16{0, 1}}
	d := chunks(t, render(t, p))["data"]
	want := []int16{1, -1, 2, -2, 3, -3}
	for i, w := range want {
		if int16(le.Uint16(d[2*i:])) != w {
			t.Fatalf("sample %d = %d, want %d", i, int16(le.Uint16(d[2*i:])), w)
		}
	}
}

func TestSmplChunkLoop(t *testing.T) {
	p := mono()
	p.HasLoop, p.LoopStart, p.LoopEnd = true, 1, 3 // BWAV loop end is exclusive
	s := chunks(t, render(t, p))["smpl"]
	if len(s) != 60 {
		t.Fatalf("smpl len %d", len(s))
	}
	if le.Uint32(s[28:]) != 1 { // numSampleLoops
		t.Fatal("expected one loop")
	}
	if le.Uint32(s[36+0:]) != 0 || le.Uint32(s[36+4:]) != 0 { // cue id, forward loop
		t.Fatal("loop type")
	}
	if le.Uint32(s[36+8:]) != 1 || le.Uint32(s[36+12:]) != 2 { // start, inclusive end = LoopEnd-1
		t.Fatalf("loop start/end = %d/%d", le.Uint32(s[44:]), le.Uint32(s[48:]))
	}
	if le.Uint32(s[8:]) != 1000000000/44100 { // sample period ns
		t.Fatalf("sample period %d", le.Uint32(s[8:]))
	}
}

func TestNoSmplWithoutLoop(t *testing.T) {
	if _, ok := chunks(t, render(t, mono()))["smpl"]; ok {
		t.Fatal("smpl chunk written without a loop")
	}
}

func TestSixChannelExtensible(t *testing.T) {
	p := &bwav.PCM{SampleRate: 48000, Channels: 6, Pan: make([]uint16, 6)}
	for i := 0; i < 6; i++ {
		p.Samples = append(p.Samples, []int16{int16(i), int16(i)})
	}
	f := chunks(t, render(t, p))["fmt "]
	if le.Uint16(f[0:]) != 0xFFFE || le.Uint16(f[2:]) != 6 || len(f) != 40 {
		t.Fatalf("fmt = % x", f)
	}
	if le.Uint32(f[20:]) != 0x3F { // 5.1 channel mask
		t.Fatalf("mask = %#x", le.Uint32(f[20:]))
	}
	if le.Uint16(f[24:]) != 1 { // PCM sub-format GUID starts with format tag 1
		t.Fatal("sub-format not PCM")
	}
}

func TestSizeMatchesWritten(t *testing.T) {
	loop := mono()
	loop.HasLoop, loop.LoopEnd = true, 3
	six := &bwav.PCM{SampleRate: 48000, Channels: 6, Pan: make([]uint16, 6)}
	for i := 0; i < 6; i++ {
		six.Samples = append(six.Samples, []int16{1, 2, 3})
	}
	for _, p := range []*bwav.PCM{mono(), loop, six} {
		if got, want := len(render(t, p)), Size(p); got != want {
			t.Errorf("Size = %d, written %d", want, got)
		}
	}
}

func TestEncodeMatchesWrite(t *testing.T) {
	p := mono()
	p.HasLoop, p.LoopEnd = true, 3
	if got, want := Encode(p), render(t, p); !bytes.Equal(got, want) || len(got) != Size(p) {
		t.Fatalf("Encode differs from Write (%d vs %d bytes, Size %d)", len(got), len(want), Size(p))
	}
}
