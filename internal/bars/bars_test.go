package bars

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"

	"barsdig/internal/synth"
)

func bw(n int) []byte {
	return synth.BWAV(synth.BWAVSpec{Codec: 1, SampleRate: 48000, FullSamples: 14, Samples: 14, Channels: [][]byte{make([]byte, n)}})
}

func entries() []synth.Entry {
	return []synth.Entry{
		{Name: "SE_B", VolumeDB: -12.5, Channels: 1, BWAV: bw(8)},
		{Name: "SE_A", Channels: 2, BWAV: bw(16), Markers: []synth.Marker{{ID: 7, Name: "JumpOK", Start: 100, Length: 5}, {ID: 8, Name: "PlayNote:28", Start: 200, Length: 0}}},
		{Name: "SE_C", Channels: 1}, // no audio
	}
}

func byName(t *testing.T, f *File, name string) Entry {
	t.Helper()
	for _, e := range f.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("entry %q not found", name)
	return Entry{}
}

func TestParseRoundTrip(t *testing.T) {
	f, err := Parse(synth.BARS(entries()))
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 0x0102 || len(f.Entries) != 3 {
		t.Fatalf("version %#x entries %d", f.Version, len(f.Entries))
	}
	for _, e := range f.Entries {
		if !e.NameVerified {
			t.Errorf("%s: name not verified", e.Name)
		}
		if e.Hash != synth.CRC(e.Name) {
			t.Errorf("%s: hash %#x", e.Name, e.Hash)
		}
		if e.AMTA.Version != 0x0500 {
			t.Errorf("%s: amta version %#x", e.Name, e.AMTA.Version)
		}
	}
	b := byName(t, f, "SE_B")
	if b.AMTA.VolumeDB != -12.5 || b.AMTA.Channels != 1 {
		t.Errorf("SE_B amta = %+v", b.AMTA)
	}
	a := byName(t, f, "SE_A")
	if a.AMTA.Channels != 2 || len(a.AMTA.Markers) != 2 {
		t.Fatalf("SE_A amta = %+v", a.AMTA)
	}
	want := Marker{ID: 7, Name: "JumpOK", Start: 100, Length: 5}
	if a.AMTA.Markers[0] != want {
		t.Errorf("marker 0 = %+v, want %+v", a.AMTA.Markers[0], want)
	}
	if a.AMTA.Markers[1].Name != "PlayNote:28" || a.AMTA.Markers[1].Start != 200 {
		t.Errorf("marker 1 = %+v", a.AMTA.Markers[1])
	}
	if len(a.AMTA.RawData) != 0x28 {
		t.Errorf("RawData len = %d, want 0x28", len(a.AMTA.RawData))
	}
}

func TestAliasResolved(t *testing.T) {
	e := entries()
	e[1].BWAV = nil
	e[1].AliasOf = 1 // SE_A shares SE_B's BWAV
	f, err := Parse(synth.BARS(e))
	if err != nil {
		t.Fatal(err)
	}
	a, b := byName(t, f, "SE_A"), byName(t, f, "SE_B")
	if a.BWAV == nil || b.BWAV == nil || len(a.BWAV) != len(b.BWAV) || a.BWAVOffset != b.BWAVOffset {
		t.Fatalf("alias not resolved: %v %v", len(a.BWAV), len(b.BWAV))
	}
	lo, hi := a, b
	if b.Index < a.Index {
		lo, hi = b, a
	}
	if lo.AliasOf != -1 || hi.AliasOf != lo.Index {
		t.Errorf("AliasOf: lo=%d hi=%d (lo.Index=%d)", lo.AliasOf, hi.AliasOf, lo.Index)
	}
	if c := byName(t, f, "SE_C"); c.AliasOf != -1 {
		t.Errorf("no-audio entry AliasOf = %d", c.AliasOf)
	}
}

func TestNoAudioEntry(t *testing.T) {
	f, err := Parse(synth.BARS(entries()))
	if err != nil {
		t.Fatal(err)
	}
	c := byName(t, f, "SE_C")
	if c.BWAV != nil || c.BWAVOffset != NoOffset {
		t.Fatalf("SE_C: BWAV=%v offset=%#x", c.BWAV != nil, c.BWAVOffset)
	}
	if byName(t, f, "SE_B").BWAV == nil {
		t.Fatal("other entries must be unaffected")
	}
}

func TestBWAVSizeIsDistanceToNextOffset(t *testing.T) {
	f, err := Parse(synth.BARS(entries()))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SE_A", "SE_B"} {
		e := byName(t, f, name)
		if string(e.BWAV[:4]) != "BWAV" || len(e.BWAV)%0x40 != 0 {
			t.Errorf("%s: bwav len %d magic %q", name, len(e.BWAV), e.BWAV[:4])
		}
	}
	// the highest-offset entry runs to EOF
	data := synth.BARS(entries())
	var last Entry
	for _, e := range f.Entries {
		if e.BWAV != nil && e.BWAVOffset > last.BWAVOffset {
			last = e
		}
	}
	if int(last.BWAVOffset)+len(last.BWAV) != len(data) {
		t.Errorf("last entry ends at %d, file is %d", int(last.BWAVOffset)+len(last.BWAV), len(data))
	}
}

func TestUnsupportedVersion(t *testing.T) {
	d := synth.BARS(entries())
	le.PutUint16(d[10:], 0x0101)
	var ue *UnsupportedError
	if _, err := Parse(d); !errors.As(err, &ue) || ue.BARS != 0x0101 {
		t.Fatalf("BARS version: err = %v", err)
	}

	d = synth.BARS(entries())
	n := int(le.Uint32(d[12:]))
	amta := int(le.Uint32(d[0x10+4*n:]))
	le.PutUint16(d[amta+6:], 0x0400)
	if _, err := Parse(d); !errors.As(err, &ue) || ue.AMTA != 0x0400 || ue.BARS != 0x0102 {
		t.Fatalf("AMTA version: err = %v", err)
	}
	if ue.Error() == "" {
		t.Fatal("empty error message")
	}
}

func TestBadNameHashReportedNotFixed(t *testing.T) {
	d := synth.BARS(entries())
	le.PutUint32(d[0x10:], le.Uint32(d[0x10:])^1) // corrupt the first stored hash
	f, err := Parse(d)
	if err != nil {
		t.Fatal(err)
	}
	bad := 0
	for _, e := range f.Entries {
		if !e.NameVerified {
			bad++
			if e.Name == "" {
				t.Error("structural name lost")
			}
		}
	}
	if bad != 1 {
		t.Fatalf("%d unverified names, want 1", bad)
	}
}

func TestTruncatedNeverPanics(t *testing.T) {
	d := synth.BARS(entries())
	for n := 0; n < len(d); n++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on prefix %d: %v", n, r)
				}
			}()
			_, _ = Parse(d[:n])
		}()
	}
}

func TestHugeCountRejected(t *testing.T) {
	d := synth.BARS(entries())
	le.PutUint32(d[12:], 0xFFFFFFF0)
	if _, err := Parse(d); err == nil {
		t.Fatal("expected error for huge entry count")
	}
}

func TestOffsetsPastEOF(t *testing.T) {
	d := synth.BARS(entries())
	n := int(le.Uint32(d[12:]))
	le.PutUint32(d[0x10+4*n:], uint32(len(d)+100)) // AMTA offset
	if _, err := Parse(d); err == nil {
		t.Fatal("AMTA offset past EOF: expected error")
	}
	d = synth.BARS(entries())
	for i := 0; i < n; i++ { // BWAV offset past EOF
		le.PutUint32(d[0x10+4*n+8*i+4:], uint32(len(d)+100))
	}
	if _, err := Parse(d); err == nil {
		t.Fatal("BWAV offset past EOF: expected error")
	}
}

func TestBadMagicAndBOM(t *testing.T) {
	d := synth.BARS(entries())
	d[0] = 'X'
	if _, err := Parse(d); err == nil {
		t.Fatal("bad magic accepted")
	}
	d = synth.BARS(entries())
	d[8], d[9] = 0xFE, 0xFF
	if _, err := Parse(d); err == nil {
		t.Fatal("big-endian BOM accepted")
	}
}

func TestRawBytesDoNotAliasInput(t *testing.T) {
	d := synth.BARS(entries())
	f, err := Parse(d)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), byName(t, f, "SE_A").AMTA.RawData...)
	for i := range d {
		d[i] = 0xEE // the caller drops or reuses the decompressed bank
	}
	if got := byName(t, f, "SE_A").AMTA.RawData; !bytes.Equal(got, want) {
		t.Fatal("AMTA.RawData still points into the input buffer; it would keep a whole decompressed bank alive")
	}
}

// many entries pointing at one AMTA block with a huge name must not make Parse
// allocate (entries x name length) bytes.
func TestSharedHugeAMTAIsBounded(t *testing.T) {
	one := synth.BARS([]synth.Entry{{Name: strings.Repeat("N", 3000), Channels: 1}})
	amta := one[0x10+12 : len(one)] // the AMTA block (single entry, no audio)
	const n = 40000
	d := make([]byte, 0x10+12*n+len(amta))
	copy(d, "BARS")
	d[8], d[9] = 0xFF, 0xFE
	le.PutUint16(d[10:], 0x0102)
	le.PutUint32(d[12:], n)
	amtaOff := 0x10 + 12*n
	for i := 0; i < n; i++ {
		le.PutUint32(d[0x10+4*i:], uint32(i))
		le.PutUint32(d[0x10+4*n+8*i:], uint32(amtaOff))
		le.PutUint32(d[0x10+4*n+8*i+4:], NoOffset)
	}
	copy(d[amtaOff:], amta)
	le.PutUint32(d[4:], uint32(len(d)))

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := Parse(d)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("expected an error: 40000 entries x 3000-byte names exceed the metadata budget")
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 256<<20 {
		t.Fatalf("Parse allocated %d MB before giving up", alloc>>20)
	}
}

func TestOverlongNameRejected(t *testing.T) {
	d := synth.BARS([]synth.Entry{{Name: strings.Repeat("N", 5000), Channels: 1}})
	if _, err := Parse(d); err == nil {
		t.Fatal("5000-byte cue name accepted")
	}
}
