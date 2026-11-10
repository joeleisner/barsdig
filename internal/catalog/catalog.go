// Package catalog builds, caches and searches the list of cues found in an
// extracted RomFS.
package catalog

import "barsdig/internal/bars"

// Loop is a loop region in samples (End is exclusive, as stored in BWAV).
type Loop struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Cue is one named sound.
type Cue struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Bank         string `json:"bank"`
	NameVerified bool   `json:"name_verified"` // BARS cues: name CRC-32 matches the stored hash

	Codec      string  `json:"codec"`
	Channels   int     `json:"channels"`
	SampleRate int     `json:"sample_rate"`
	Samples    int     `json:"samples"`
	Duration   float64 `json:"duration"` // seconds of the audio that will be played
	Loop       *Loop   `json:"loop,omitempty"`

	BarsPath   string `json:"bars_path,omitempty"` // relative path of the BARS file; empty for standalone cues
	EntryIndex int    `json:"entry_index"`
	// StreamPath is the standalone BWAV: the cue's own file, or the full stream a
	// prefetch stub is replaced by.
	StreamPath     string `json:"stream_path,omitempty"`
	PrefetchStub   bool   `json:"prefetch_stub"`    // the BARS-embedded copy is a prefetch stub
	UsesFullStream bool   `json:"uses_full_stream"` // StreamPath replaces the stub
	NoAudio        bool   `json:"no_audio"`
	AliasOf        string `json:"alias_of,omitempty"` // cue ID sharing the same audio block

	AMTA        *bars.AMTA `json:"amta,omitempty"`
	BarsVersion string     `json:"bars_version,omitempty"`
}

// Problem is something that went wrong while scanning, reported rather than fatal.
type Problem struct {
	Path  string `json:"path"`
	Entry int    `json:"entry"` // -1 when the whole file is affected
	Msg   string `json:"msg"`
}

// Catalog is the set of cues plus scan problems.
type Catalog struct {
	Cues     []*Cue    `json:"cues"`
	Problems []Problem `json:"problems"`
	byID     map[string]*Cue
}

// Options configure Build.
type Options struct {
	Roots []string // relative directories to scan; default Sound/Resource and Voice/Resource
}

// DefaultRoots are scanned when Options.Roots is empty.
var DefaultRoots = []string{"Sound/Resource", "Voice/Resource"}

func (c *Catalog) index() {
	c.byID = make(map[string]*Cue, len(c.Cues))
	for _, cue := range c.Cues {
		c.byID[cue.ID] = cue
	}
}

// Get returns the cue with the given ID.
func (c *Catalog) Get(id string) (*Cue, bool) {
	cue, ok := c.byID[id]
	return cue, ok
}
