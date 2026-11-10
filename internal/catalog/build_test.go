package catalog

import (
	"encoding/binary"
	"strings"
	"testing"

	"barsdig/internal/synth"
)

func dspBWAV(samples, full uint32, prefetch bool, loop *[2]int32) []byte {
	return synth.BWAV(synth.BWAVSpec{Codec: 1, SampleRate: 48000, FullSamples: full, Samples: samples, Prefetch: prefetch, Loop: loop,
		Channels: [][]byte{make([]byte, 8)}})
}

func build(t *testing.T, m *memSource) *Catalog {
	t.Helper()
	c, err := Build(m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func find(t *testing.T, c *Catalog, id string) *Cue {
	t.Helper()
	cue, ok := c.Get(id)
	if !ok {
		var ids []string
		for _, x := range c.Cues {
			ids = append(ids, x.ID)
		}
		t.Fatalf("cue %q not found among %v", id, ids)
	}
	return cue
}

func TestBarsEntriesBecomeCues(t *testing.T) {
	loop := [2]int32{100, 4800}
	m := newMem().
		put("Sound/Resource/Obj_X.bars", synth.BARS([]synth.Entry{{Name: "SE_A", Channels: 1, VolumeDB: -3, BWAV: dspBWAV(48000, 48000, false, &loop)}})).
		put("Sound/Resource/Sub/Obj_Y.bars.zs", synth.BARS([]synth.Entry{{Name: "SE_B", Channels: 1, BWAV: dspBWAV(24000, 24000, false, nil)}}))
	c := build(t, m)
	a := find(t, c, "Obj_X/SE_A")
	if a.Name != "SE_A" || a.Bank != "Obj_X" || a.Codec != "dsp-adpcm" || a.Channels != 1 || a.SampleRate != 48000 ||
		a.Samples != 48000 || a.Duration != 1.0 || a.BarsPath != "Sound/Resource/Obj_X.bars" || a.EntryIndex != 0 ||
		!a.NameVerified || a.BarsVersion != "1.2" || a.AMTA == nil || a.AMTA.VolumeDB != -3 || a.NoAudio {
		t.Fatalf("cue = %+v", a)
	}
	if a.Loop == nil || a.Loop.Start != 100 || a.Loop.End != 4800 {
		t.Fatalf("loop = %+v", a.Loop)
	}
	b := find(t, c, "Sub/Obj_Y/SE_B")
	if b.Bank != "Sub/Obj_Y" || b.Loop != nil || b.Duration != 0.5 {
		t.Fatalf("cue = %+v", b)
	}
}

func TestStandaloneStreamCues(t *testing.T) {
	m := newMem().
		put("Sound/Resource/Stream/Ambient_Foo.bwav", dspBWAV(96000, 96000, false, nil)).
		put("Voice/Resource/Stream/V_1.bwav", dspBWAV(48000, 48000, false, nil))
	c := build(t, m)
	f := find(t, c, "Stream/Ambient_Foo")
	if f.Bank != "Stream" || f.StreamPath != "Sound/Resource/Stream/Ambient_Foo.bwav" || f.BarsPath != "" || f.Duration != 2.0 || f.AMTA != nil {
		t.Fatalf("cue = %+v", f)
	}
	v := find(t, c, "Voice/Stream/V_1")
	if v.Bank != "Voice/Stream" {
		t.Fatalf("voice cue = %+v", v)
	}
}

func TestPrefetchLinkedToFullStream(t *testing.T) {
	m := newMem().
		put("Sound/Resource/Ambient_Bank.bars", synth.BARS([]synth.Entry{{Name: "Ambient_Foo", Channels: 2, BWAV: dspBWAV(14336, 735894, true, nil)}})).
		put("Sound/Resource/Stream/Ambient_Foo.bwav", dspBWAV(735894, 735894, false, nil))
	c := build(t, m)
	cue := find(t, c, "Ambient_Bank/Ambient_Foo")
	if !cue.PrefetchStub || !cue.UsesFullStream || cue.StreamPath != "Sound/Resource/Stream/Ambient_Foo.bwav" || cue.Samples != 735894 {
		t.Fatalf("cue = %+v", cue)
	}
	n := 0
	for _, x := range c.Cues {
		if x.Name == "Ambient_Foo" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d cues named Ambient_Foo, want 1 (standalone twin must not be listed again)", n)
	}
}

func TestPrefetchWithoutStreamIsFlaggedStub(t *testing.T) {
	m := newMem().put("Sound/Resource/B.bars", synth.BARS([]synth.Entry{{Name: "Ambient_X", Channels: 1, BWAV: dspBWAV(14336, 99999, true, nil)}}))
	cue := find(t, build(t, m), "B/Ambient_X")
	if !cue.PrefetchStub || cue.UsesFullStream || cue.StreamPath != "" || cue.Samples != 14336 {
		t.Fatalf("cue = %+v", cue)
	}
}

func TestAliasMarked(t *testing.T) {
	e := []synth.Entry{{Name: "SE_B", Channels: 1, BWAV: dspBWAV(14, 14, false, nil)}, {Name: "SE_A", Channels: 1, AliasOf: 1}}
	c := build(t, newMem().put("Sound/Resource/B.bars", synth.BARS(e)))
	a, b := find(t, c, "B/SE_A"), find(t, c, "B/SE_B")
	if a.NoAudio || b.NoAudio || a.Samples != 14 {
		t.Fatalf("alias lost audio: %+v", a)
	}
	n := 0
	if a.AliasOf != "" {
		n++
		if a.AliasOf != "B/SE_B" {
			t.Errorf("a.AliasOf = %q", a.AliasOf)
		}
	}
	if b.AliasOf != "" {
		n++
		if b.AliasOf != "B/SE_A" {
			t.Errorf("b.AliasOf = %q", b.AliasOf)
		}
	}
	if n != 1 {
		t.Fatalf("exactly one of the pair should be marked alias, got %d", n)
	}
}

func TestNoAudioEntryVisible(t *testing.T) {
	e := []synth.Entry{{Name: "SE_Silent", Channels: 1}, {Name: "SE_Ok", Channels: 1, BWAV: dspBWAV(14, 14, false, nil)}}
	c := build(t, newMem().put("Sound/Resource/B.bars", synth.BARS(e)))
	s := find(t, c, "B/SE_Silent")
	if !s.NoAudio || s.Duration != 0 {
		t.Fatalf("cue = %+v", s)
	}
	if find(t, c, "B/SE_Ok").NoAudio {
		t.Fatal("neighbour affected")
	}
	if len(c.Problems) != 0 {
		t.Fatalf("no-audio entry should not be a problem: %v", c.Problems)
	}
}

func TestEmptyAndNonBWAVStandaloneBecomeProblems(t *testing.T) {
	m := newMem().
		put("Sound/Resource/Stream/empty.bwav", nil).
		put("Sound/Resource/Stream/junk.bwav", []byte("this is not a bwav file at all")).
		put("Sound/Resource/Stream/good.bwav", dspBWAV(14, 14, false, nil))
	c := build(t, m)
	find(t, c, "Stream/good")
	if len(c.Cues) != 1 || len(c.Problems) != 2 {
		t.Fatalf("%d cues, problems = %+v", len(c.Cues), c.Problems)
	}
	for _, p := range c.Problems {
		if !strings.HasSuffix(p.Path, "empty.bwav") && !strings.HasSuffix(p.Path, "junk.bwav") || p.Entry != -1 || p.Msg == "" {
			t.Errorf("problem = %+v", p)
		}
	}
}

func TestUnsupportedBarsVersionIsProblemNotAbort(t *testing.T) {
	bad := synth.BARS([]synth.Entry{{Name: "SE_A", Channels: 1}})
	binary.LittleEndian.PutUint16(bad[10:], 0x0101)
	m := newMem().
		put("Sound/Resource/Old.bars", bad).
		put("Sound/Resource/New.bars", synth.BARS([]synth.Entry{{Name: "SE_B", Channels: 1}}))
	c := build(t, m)
	find(t, c, "New/SE_B")
	if len(c.Problems) != 1 || !strings.Contains(c.Problems[0].Msg, "unsupported") || c.Problems[0].Path != "Sound/Resource/Old.bars" {
		t.Fatalf("problems = %+v", c.Problems)
	}
}

func TestNameMismatchIsProblemButCueKept(t *testing.T) {
	d := synth.BARS([]synth.Entry{{Name: "SE_A", Channels: 1}})
	binary.LittleEndian.PutUint32(d[0x10:], 0xDEADBEEF)
	c := build(t, newMem().put("Sound/Resource/B.bars", d))
	cue := find(t, c, "B/SE_A")
	if cue.NameVerified || len(c.Problems) != 1 || c.Problems[0].Entry != 0 {
		t.Fatalf("cue %+v problems %+v", cue, c.Problems)
	}
}

func TestIDCollisionSuffix(t *testing.T) {
	e := []synth.Entry{{Name: "Dup", Channels: 1}, {Name: "Dup", Channels: 1}}
	c := build(t, newMem().put("Sound/Resource/B.bars", synth.BARS(e)))
	find(t, c, "B/Dup")
	find(t, c, "B/Dup~2")
}

func TestMissingVoiceRootOK(t *testing.T) {
	c := build(t, newMem().put("Sound/Resource/B.bars", synth.BARS([]synth.Entry{{Name: "A", Channels: 1}})))
	if len(c.Cues) != 1 {
		t.Fatalf("%d cues", len(c.Cues))
	}
}

// Voice banks embed header-only BWAVs: this file's sample count is 0, the full
// count is in the header, and the prefetch flag is NOT set. The audio is the
// Stream/ file of the same name.
func TestDummyEmbeddedBWAVLinksToFullStream(t *testing.T) {
	dummy := synth.BWAV(synth.BWAVSpec{Codec: 2, SampleRate: 48000, FullSamples: 31215, Samples: 0, Channels: [][]byte{nil}})
	m := newMem().
		put("Voice/Resource/V.bars", synth.BARS([]synth.Entry{{Name: "Line_a", Channels: 1, BWAV: dummy}})).
		put("Voice/Resource/Stream/Line_a.bwav", dspBWAV(31215, 31215, false, nil))
	c := build(t, m)
	cue := find(t, c, "Voice/V/Line_a")
	if !cue.PrefetchStub || !cue.UsesFullStream || cue.NoAudio || cue.Samples != 31215 || cue.StreamPath != "Voice/Resource/Stream/Line_a.bwav" {
		t.Fatalf("cue = %+v", cue)
	}
	n := 0
	for _, x := range c.Cues {
		if x.Name == "Line_a" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d cues named Line_a, want 1", n)
	}
}

func TestDummyEmbeddedBWAVWithoutStreamHasNoAudio(t *testing.T) {
	dummy := synth.BWAV(synth.BWAVSpec{Codec: 2, SampleRate: 48000, FullSamples: 31215, Samples: 0, Channels: [][]byte{nil}})
	c := build(t, newMem().put("Voice/Resource/V.bars", synth.BARS([]synth.Entry{{Name: "Line_a", Channels: 1, BWAV: dummy}})))
	cue := find(t, c, "Voice/V/Line_a")
	if !cue.NoAudio || !cue.PrefetchStub || cue.UsesFullStream {
		t.Fatalf("cue = %+v", cue)
	}
}
