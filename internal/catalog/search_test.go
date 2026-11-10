package catalog

import (
	"reflect"
	"testing"

	"barsdig/internal/synth"
)

func searchFixture(t *testing.T) *Catalog {
	loop := [2]int32{0, 48000}
	m := newMem().
		put("Sound/Resource/Obj_Widget.bars", synth.BARS([]synth.Entry{
			{Name: "SE_Obj_Widget_Appear00", Channels: 1, BWAV: dspBWAV(24000, 24000, false, nil)},
			{Name: "SE_Obj_Widget_Land00", Channels: 1, BWAV: dspBWAV(12000, 12000, false, nil)},
		})).
		put("Sound/Resource/Ambient_Bank.bars", synth.BARS([]synth.Entry{
			{Name: "Ambient_Field", Channels: 2, BWAV: synth.BWAV(synth.BWAVSpec{Codec: 1, SampleRate: 48000, FullSamples: 480000, Samples: 480000, Loop: &loop,
				Channels: [][]byte{make([]byte, 8), make([]byte, 8)}})},
		})).
		put("Sound/Resource/Stream/Fanfare.bwav", synth.BWAV(synth.BWAVSpec{Codec: 2, SampleRate: 48000, FullSamples: 96000, Samples: 96000, Channels: [][]byte{make([]byte, 8)}}))
	return build(t, m)
}

func names(cues []*Cue) []string {
	var n []string
	for _, c := range cues {
		n = append(n, c.Name)
	}
	return n
}

func TestSearchSubstringCaseInsensitive(t *testing.T) {
	c := searchFixture(t)
	if got := names(c.Search(Query{Text: "widget"})); !reflect.DeepEqual(got, []string{"SE_Obj_Widget_Appear00", "SE_Obj_Widget_Land00"}) {
		t.Fatalf("got %v", got)
	}
	if got := names(c.Search(Query{Text: "LAND"})); !reflect.DeepEqual(got, []string{"SE_Obj_Widget_Land00"}) {
		t.Fatalf("got %v", got)
	}
	if got := names(c.Search(Query{Text: "ambient_bank"})); !reflect.DeepEqual(got, []string{"Ambient_Field"}) { // matches the bank
		t.Fatalf("bank match: %v", got)
	}
	if got := names(c.Search(Query{Text: "widget appear"})); !reflect.DeepEqual(got, []string{"SE_Obj_Widget_Appear00"}) { // all terms must match
		t.Fatalf("multi-term: %v", got)
	}
	if got := c.Search(Query{}); len(got) != 4 {
		t.Fatalf("empty query returned %d", len(got))
	}
}

func TestSearchFilters(t *testing.T) {
	c := searchFixture(t)
	check := func(q Query, want ...string) {
		t.Helper()
		if got := names(c.Search(q)); !reflect.DeepEqual(got, want) {
			t.Errorf("%+v: got %v, want %v", q, got, want)
		}
	}
	check(Query{Banks: []string{"Ambient_Bank"}}, "Ambient_Field")
	check(Query{Codecs: []string{"opus"}}, "Fanfare")
	check(Query{MinSec: 1.5}, "Ambient_Field", "Fanfare") // sorted by name: Ambient_Field < Fanfare
	check(Query{MaxSec: 0.6}, "SE_Obj_Widget_Appear00", "SE_Obj_Widget_Land00")
	check(Query{MinSec: 0.3, MaxSec: 0.6}, "SE_Obj_Widget_Appear00")
	check(Query{LoopOnly: true}, "Ambient_Field")
	check(Query{Channels: 2}, "Ambient_Field")
}

func TestSortStable(t *testing.T) {
	c := searchFixture(t)
	if got := names(c.Search(Query{Sort: "duration"})); !reflect.DeepEqual(got, []string{"SE_Obj_Widget_Land00", "SE_Obj_Widget_Appear00", "Fanfare", "Ambient_Field"}) {
		t.Fatalf("duration asc: %v", got)
	}
	if got := names(c.Search(Query{Sort: "duration", Desc: true})); !reflect.DeepEqual(got, []string{"Ambient_Field", "Fanfare", "SE_Obj_Widget_Appear00", "SE_Obj_Widget_Land00"}) {
		t.Fatalf("duration desc: %v", got)
	}
	if got := names(c.Search(Query{Sort: "channels"})); got[0] == "Ambient_Field" {
		t.Fatalf("channels asc put stereo first: %v", got)
	}
	if got := names(c.Search(Query{Sort: "bank"})); got[0] != "Ambient_Field" {
		t.Fatalf("bank asc: %v", got)
	}
}

func TestFacets(t *testing.T) {
	f := searchFixture(t).Facets()
	if !reflect.DeepEqual(f.Banks, []string{"Ambient_Bank", "Obj_Widget", "Stream"}) ||
		!reflect.DeepEqual(f.Codecs, []string{"dsp-adpcm", "opus"}) ||
		!reflect.DeepEqual(f.Channels, []int{1, 2}) || f.MaxDuration != 10 {
		t.Fatalf("facets = %+v", f)
	}
}
