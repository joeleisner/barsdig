package catalog

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"barsdig/internal/bars"
	"barsdig/internal/bwav"
	"barsdig/internal/source"
)

const headBytes = 0x800 // enough for the header of a BWAV with up to 26 channels

type standalone struct {
	rel  string
	info *bwav.Info
	cue  *Cue
	used bool
}

// bankPrefix is "" for the Sound tree and "<Dir>/" for others (Voice/Resource -> "Voice/").
func bankPrefix(root string) string {
	first := strings.SplitN(root, "/", 2)[0]
	if first == "Sound" || first == "" {
		return ""
	}
	return first + "/"
}

func fillAudio(c *Cue, info *bwav.Info) {
	ch := info.Channels[0]
	c.Codec = info.CodecName()
	c.Channels = len(info.Channels)
	c.SampleRate = int(ch.SampleRate)
	c.Samples = int(ch.Samples)
	if c.SampleRate > 0 {
		c.Duration = float64(c.Samples) / float64(c.SampleRate)
	}
	c.Loop = nil
	if s, e, ok := info.Loop(); ok {
		c.Loop = &Loop{Start: s, End: e}
	}
}

// Build scans the source and returns the catalog. Problems with individual
// files or entries are recorded in Catalog.Problems, never returned as errors.
func Build(src source.Source, opt Options) (*Catalog, error) {
	roots := opt.Roots
	if len(roots) == 0 {
		roots = DefaultRoots
	}
	cat := &Catalog{Problems: []Problem{}}
	problem := func(p string, entry int, format string, a ...any) {
		cat.Problems = append(cat.Problems, Problem{Path: p, Entry: entry, Msg: fmt.Sprintf(format, a...)})
	}

	aliasTarget := map[*Cue]*Cue{} // alias cue -> the cue whose audio it shares
	var all []*standalone
	var cues []*Cue
	for _, root := range roots {
		prefix := bankPrefix(root)
		streams := map[string]*standalone{} // name -> top-level Stream/ file under this root
		var barsFiles []string
		err := src.Walk(root, func(e source.FileEntry) error {
			rel := strings.TrimPrefix(e.Rel, root+"/")
			switch {
			case strings.HasSuffix(rel, ".bars") || strings.HasSuffix(rel, ".bars.zs"):
				barsFiles = append(barsFiles, e.Rel)
			case strings.HasSuffix(rel, ".bwav"):
				head, err := src.ReadHead(e.Rel, headBytes)
				if err != nil {
					problem(e.Rel, -1, "read failed: %v", err)
					return nil
				}
				info, err := bwav.ParseInfo(head)
				if err != nil {
					problem(e.Rel, -1, "not a usable BWAV: %v", err)
					return nil
				}
				dir := path.Dir(rel)
				if dir == "." {
					dir = ""
				}
				bank := strings.TrimSuffix(prefix+dir, "/")
				if bank == "" {
					bank = "(root)"
				}
				c := &Cue{Name: strings.TrimSuffix(path.Base(rel), ".bwav"), Bank: bank, StreamPath: e.Rel, EntryIndex: -1}
				fillAudio(c, info)
				s := &standalone{rel: e.Rel, info: info, cue: c}
				all = append(all, s)
				if dir == "Stream" {
					streams[c.Name] = s
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}

		for _, rel := range barsFiles {
			data, err := src.Read(rel)
			if err != nil {
				problem(rel, -1, "read failed: %v", err)
				continue
			}
			f, err := bars.Parse(data)
			if err != nil {
				problem(rel, -1, "%v", err)
				continue
			}
			name := strings.TrimPrefix(rel, root+"/")
			name = strings.TrimSuffix(strings.TrimSuffix(name, ".zs"), ".bars")
			bank := prefix + name
			made := make([]*Cue, len(f.Entries))
			for i, e := range f.Entries {
				amta := e.AMTA
				c := &Cue{
					Name: e.Name, Bank: bank, NameVerified: e.NameVerified, BarsPath: rel, EntryIndex: e.Index,
					AMTA: &amta, BarsVersion: fmt.Sprintf("%d.%d", f.Version>>8, f.Version&0xFF),
				}
				made[i] = c
				if !e.NameVerified {
					problem(rel, e.Index, "name %q does not match its stored CRC-32 %#08x", e.Name, e.Hash)
				}
				if e.BWAV == nil {
					c.NoAudio = true
					continue
				}
				info, err := bwav.ParseInfo(e.BWAV)
				if err != nil {
					c.NoAudio = true
					problem(rel, e.Index, "%s: embedded BWAV unusable: %v", e.Name, err)
					continue
				}
				fillAudio(c, info)
				// An embedded BWAV that holds less than the full sound is a stub: either a
				// flagged prefetch clip, or (Voice banks) a header-only dummy with 0 samples.
				if ch := info.Channels[0]; info.Prefetch || ch.Samples < ch.FullSamples {
					c.PrefetchStub = true
					if s, ok := streams[e.Name]; ok {
						s.used = true
						c.UsesFullStream, c.StreamPath = true, s.rel
						fillAudio(c, s.info)
					} else if ch.Samples == 0 {
						c.NoAudio = true // nothing embedded and no full stream to play
					}
				}
			}
			for i, e := range f.Entries {
				if e.AliasOf >= 0 {
					aliasTarget[made[i]] = made[e.AliasOf]
				}
			}
			cues = append(cues, made...)
		}
	}
	for _, s := range all {
		if !s.used {
			cues = append(cues, s.cue)
		}
	}

	sort.SliceStable(cues, func(i, j int) bool {
		a, b := cues[i], cues[j]
		if a.Bank != b.Bank {
			return a.Bank < b.Bank
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.BarsPath != b.BarsPath {
			return a.BarsPath < b.BarsPath
		}
		return a.EntryIndex < b.EntryIndex
	})
	seen := map[string]int{}
	for _, c := range cues {
		id := c.Bank + "/" + c.Name
		seen[id]++
		if n := seen[id]; n > 1 {
			id = fmt.Sprintf("%s~%d", id, n)
		}
		c.ID = id
	}
	for c, target := range aliasTarget {
		c.AliasOf = target.ID
	}
	cat.Cues = cues
	cat.index()
	return cat, nil
}
