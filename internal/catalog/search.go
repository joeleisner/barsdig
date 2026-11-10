package catalog

import (
	"slices"
	"sort"
	"strings"
)

// Query filters and orders cues. Unset fields do not filter.
type Query struct {
	Text           string // case-insensitive; every whitespace-separated term must appear in the name or bank
	Banks, Codecs  []string
	MinSec, MaxSec float64 // MaxSec 0 means no upper bound
	LoopOnly       bool
	Channels       int    // 0 = any
	Sort           string // name (default) | bank | duration | codec | channels
	Desc           bool
}

// Search returns the matching cues in a stable order.
func (c *Catalog) Search(q Query) []*Cue {
	terms := strings.Fields(strings.ToLower(q.Text))
	var out []*Cue
	for _, cue := range c.Cues {
		if len(q.Banks) > 0 && !slices.Contains(q.Banks, cue.Bank) {
			continue
		}
		if len(q.Codecs) > 0 && !slices.Contains(q.Codecs, cue.Codec) {
			continue
		}
		if q.MinSec > 0 && cue.Duration < q.MinSec {
			continue
		}
		if q.MaxSec > 0 && cue.Duration > q.MaxSec {
			continue
		}
		if q.LoopOnly && cue.Loop == nil {
			continue
		}
		if q.Channels > 0 && cue.Channels != q.Channels {
			continue
		}
		if len(terms) > 0 {
			hay := strings.ToLower(cue.Name + " " + cue.Bank)
			ok := true
			for _, t := range terms {
				if !strings.Contains(hay, t) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
		}
		out = append(out, cue)
	}
	less := func(a, b *Cue) int {
		switch q.Sort {
		case "bank":
			return strings.Compare(a.Bank, b.Bank)
		case "duration":
			switch {
			case a.Duration < b.Duration:
				return -1
			case a.Duration > b.Duration:
				return 1
			}
		case "codec":
			return strings.Compare(a.Codec, b.Codec)
		case "channels":
			return a.Channels - b.Channels
		}
		return strings.Compare(a.Name, b.Name)
	}
	sort.SliceStable(out, func(i, j int) bool {
		r := less(out[i], out[j])
		if r == 0 {
			r = strings.Compare(out[i].ID, out[j].ID)
		}
		if q.Desc {
			return r > 0
		}
		return r < 0
	})
	return out
}

// Facets lists the values available for filtering.
type Facets struct {
	Banks       []string `json:"banks"`
	Codecs      []string `json:"codecs"`
	Channels    []int    `json:"channels"`
	MaxDuration float64  `json:"max_duration"`
}

// Facets summarises the catalog for building filter controls.
func (c *Catalog) Facets() Facets {
	banks, codecs, chans := map[string]bool{}, map[string]bool{}, map[int]bool{}
	f := Facets{Banks: []string{}, Codecs: []string{}, Channels: []int{}}
	for _, cue := range c.Cues {
		banks[cue.Bank], codecs[cue.Codec] = true, true
		if cue.Channels > 0 {
			chans[cue.Channels] = true
		}
		f.MaxDuration = max(f.MaxDuration, cue.Duration)
	}
	for b := range banks {
		f.Banks = append(f.Banks, b)
	}
	for k := range codecs {
		if k != "" {
			f.Codecs = append(f.Codecs, k)
		}
	}
	for n := range chans {
		f.Channels = append(f.Channels, n)
	}
	sort.Strings(f.Banks)
	sort.Strings(f.Codecs)
	sort.Ints(f.Channels)
	return f
}
