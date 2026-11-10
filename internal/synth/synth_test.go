package synth

import (
	"hash/crc32"
	"testing"
)

func sampleEntries() []Entry {
	b0 := BWAV(BWAVSpec{Codec: 1, SampleRate: 48000, FullSamples: 14, Samples: 14, Channels: [][]byte{make([]byte, 8)}})
	b1 := BWAV(BWAVSpec{Codec: 1, SampleRate: 32000, FullSamples: 28, Samples: 28, Channels: [][]byte{make([]byte, 16)}})
	return []Entry{
		{Name: "SE_B", VolumeDB: -12.5, Channels: 1, BWAV: b0},
		{Name: "SE_A", Channels: 2, BWAV: b1, Markers: []Marker{{ID: 7, Name: "JumpOK", Start: 100, Length: 5}}},
		{Name: "SE_C", Channels: 1}, // no audio
	}
}

func TestBARSHeader(t *testing.T) {
	d := BARS(sampleEntries())
	if string(d[0:4]) != "BARS" {
		t.Fatalf("magic = %q", d[0:4])
	}
	if le.Uint32(d[4:]) != uint32(len(d)) {
		t.Fatalf("size field %d != len %d", le.Uint32(d[4:]), len(d))
	}
	if d[8] != 0xFF || d[9] != 0xFE {
		t.Fatalf("BOM bytes = % x", d[8:10])
	}
	if le.Uint16(d[10:]) != 0x0102 {
		t.Fatalf("version = %#x", le.Uint16(d[10:]))
	}
	if le.Uint32(d[12:]) != 3 {
		t.Fatalf("count = %d", le.Uint32(d[12:]))
	}
	for i := 1; i < 3; i++ {
		if le.Uint32(d[0x10+4*(i-1):]) >= le.Uint32(d[0x10+4*i:]) {
			t.Fatalf("hash table not ascending at %d", i)
		}
	}
}

func TestBARSNameAtBlobEnd(t *testing.T) {
	d := BARS(sampleEntries())
	n := int(le.Uint32(d[12:]))
	pairs := 0x10 + 4*n
	for i := 0; i < n; i++ {
		amta := int(le.Uint32(d[pairs+8*i:]))
		if string(d[amta:amta+4]) != "AMTA" || le.Uint16(d[amta+6:]) != 0x0500 {
			t.Fatalf("entry %d: bad AMTA header", i)
		}
		name := d[amta+0x24+int(le.Uint32(d[amta+0x24:])):]
		name = name[:indexNUL(name)]
		if crc32.ChecksumIEEE(name) != le.Uint32(d[0x10+4*i:]) {
			t.Fatalf("entry %d: name %q does not hash to table entry", i, name)
		}
		if le.Uint32(d[amta+0x28:]) != le.Uint32(d[0x10+4*i:]) {
			t.Fatalf("entry %d: blob name crc mismatch", i)
		}
	}
}

func indexNUL(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return len(b)
}

func TestAliasSharesOffset(t *testing.T) {
	e := sampleEntries()
	e[1].BWAV = nil
	e[1].AliasOf = 1 // 1-based: share entry 0's BWAV
	d := BARS(e)
	n := int(le.Uint32(d[12:]))
	pairs := 0x10 + 4*n
	seen := map[uint32]int{}
	for i := 0; i < n; i++ {
		seen[le.Uint32(d[pairs+8*i+4:])]++
	}
	shared := false
	for off, c := range seen {
		if c == 2 && off != 0xFFFFFFFF {
			shared = true
		}
	}
	if !shared {
		t.Fatalf("no shared BWAV offset: %v", seen)
	}
}

func TestNoAudioOffset(t *testing.T) {
	d := BARS(sampleEntries())
	n := int(le.Uint32(d[12:]))
	pairs := 0x10 + 4*n
	found := false
	for i := 0; i < n; i++ {
		if le.Uint32(d[pairs+8*i+4:]) == 0xFFFFFFFF {
			found = true
		}
	}
	if !found {
		t.Fatal("no entry with offset 0xFFFFFFFF")
	}
}

func TestBWAVHeader(t *testing.T) {
	loop := [2]int32{10, 20}
	d := BWAV(BWAVSpec{Codec: 1, SampleRate: 48000, FullSamples: 100, Samples: 14336, Prefetch: true, Loop: &loop,
		Coefs: [][16]int16{{1, 2, 3}}, Channels: [][]byte{make([]byte, 8)}})
	if string(d[0:4]) != "BWAV" || le.Uint16(d[0x0C:]) != 1 || le.Uint16(d[0x0E:]) != 1 {
		t.Fatalf("header: % x", d[:0x10])
	}
	ch := d[0x10:]
	if le.Uint16(ch[0:]) != 1 || le.Uint32(ch[4:]) != 48000 || le.Uint32(ch[8:]) != 100 || le.Uint32(ch[0xC:]) != 14336 {
		t.Fatalf("channel header: % x", ch[:0x10])
	}
	if int16(le.Uint16(ch[0x12:])) != 2 {
		t.Fatal("coef[1] wrong")
	}
	if int32(le.Uint32(ch[0x3C:])) != 20 || int32(le.Uint32(ch[0x40:])) != 10 {
		t.Fatal("loop end/start wrong")
	}
	if off := le.Uint32(ch[0x34:]); off%0x40 != 0 || off < 0x10+0x4C {
		t.Fatalf("data offset %#x", off)
	}
	nl := BWAV(BWAVSpec{Codec: 1, SampleRate: 1, Samples: 1, FullSamples: 1, Channels: [][]byte{{0}}})
	if int32(le.Uint32(nl[0x10+0x3C:])) != -1 {
		t.Fatal("no loop should store -1 loop end")
	}
}
