// Package render turns a catalog cue into playable/exportable bytes: WAV for
// PCM16 and DSP-ADPCM, Ogg Opus for Opus. Shared by the server and export.
package render

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"barsdig/internal/bars"
	"barsdig/internal/bwav"
	"barsdig/internal/catalog"
	"barsdig/internal/ogg"
	"barsdig/internal/source"
	"barsdig/internal/wav"
)

// Audio is a rendered cue.
type Audio struct {
	ContentType string
	Ext         string // "wav" or "opus"
	Data        []byte
	Stub        bool // true if a prefetch stub was used because no full stream exists
}

// ErrNoAudio is returned for cues without audio.
var ErrNoAudio = errors.New("cue has no audio")

const bankCacheSize = 8

// Renderer renders cues from a source, keeping a few parsed banks in memory so
// repeated plays do not re-decompress. It is safe for concurrent use.
type Renderer struct {
	src   source.Source
	mu    sync.Mutex
	banks map[string]*bars.File
	order []string
}

// New returns a Renderer reading from src.
func New(src source.Source) *Renderer {
	return &Renderer{src: src, banks: map[string]*bars.File{}}
}

// Cue is a convenience for one-off rendering.
func Cue(src source.Source, c *catalog.Cue) (*Audio, error) { return New(src).Cue(c) }

func (r *Renderer) bank(path string) (*bars.File, error) {
	r.mu.Lock()
	f, ok := r.banks[path]
	r.mu.Unlock()
	if ok {
		return f, nil
	}
	data, err := r.src.Read(path)
	if err != nil {
		return nil, err
	}
	f, err = bars.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.banks[path]; !ok {
		r.banks[path] = f
		r.order = append(r.order, path)
		if len(r.order) > bankCacheSize {
			delete(r.banks, r.order[0])
			r.order = r.order[1:]
		}
	}
	return f, nil
}

// Cue renders one cue. A prefetch stub is replaced by its full stream when the
// catalog linked one; otherwise the stub is rendered and Audio.Stub is set.
func (r *Renderer) Cue(c *catalog.Cue) (*Audio, error) {
	if c.NoAudio {
		return nil, ErrNoAudio
	}
	var data []byte
	stub := false
	switch {
	case c.StreamPath != "" && (c.BarsPath == "" || c.UsesFullStream):
		d, err := r.src.Read(c.StreamPath)
		if err != nil {
			return nil, err
		}
		data = d
	case c.BarsPath != "":
		f, err := r.bank(c.BarsPath)
		if err != nil {
			return nil, err
		}
		if c.EntryIndex < 0 || c.EntryIndex >= len(f.Entries) {
			return nil, fmt.Errorf("%s: entry %d out of range (catalog is stale?)", c.BarsPath, c.EntryIndex)
		}
		e := f.Entries[c.EntryIndex]
		if e.Name != c.Name {
			return nil, fmt.Errorf("%s: entry %d is %q, catalog says %q (catalog is stale?)", c.BarsPath, c.EntryIndex, e.Name, c.Name)
		}
		if e.BWAV == nil {
			return nil, ErrNoAudio
		}
		data, stub = e.BWAV, c.PrefetchStub
	default:
		return nil, ErrNoAudio
	}

	info, err := bwav.ParseInfo(data)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	switch info.Channels[0].Codec {
	case bwav.CodecOpus:
		st, err := bwav.OpusStreams(data)
		if err != nil {
			return nil, err
		}
		if err := ogg.WriteOpus(&buf, st); err != nil {
			return nil, err
		}
		return &Audio{ContentType: "audio/ogg", Ext: "opus", Data: buf.Bytes(), Stub: stub}, nil
	default:
		p, err := bwav.Decode(data) // unsupported codecs yield *bwav.UnsupportedCodecError
		if err != nil {
			return nil, err
		}
		return &Audio{ContentType: "audio/wav", Ext: "wav", Data: wav.Encode(p), Stub: stub}, nil
	}
}
