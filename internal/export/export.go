// Package export writes picked cues to disk as <out>/<bank>/<cue>.<ext> with a
// JSON sidecar next to each file.
package export

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"barsdig/internal/catalog"
	"barsdig/internal/render"
	"barsdig/internal/source"
)

// Options configure Run.
type Options struct {
	OutDir      string
	Force       bool // overwrite existing files
	ToolVersion string
}

// Result is the outcome for one cue.
type Result struct {
	CueID string
	Files []string // absolute-or-OutDir-relative paths written (audio, then sidecar)
	Err   error
}

const maxNameBytes = 200

var reserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}

func init() {
	for i := 1; i <= 9; i++ {
		reserved[fmt.Sprintf("COM%d", i)] = true
		reserved[fmt.Sprintf("LPT%d", i)] = true
	}
}

// SafeName makes s usable as a single path element on common filesystems: path
// separators, control characters and Windows-illegal characters become "_",
// trailing dots and spaces are dropped, reserved device names get a "_"
// prefix, and overlong names are truncated. Ordinary names are unchanged.
func SafeName(s string) string {
	if s != "" && strings.Trim(s, ".") == "" {
		return strings.Repeat("_", len(s))
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7F, strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.TrimRight(b.String(), ". ")
	if name == "" {
		return "_"
	}
	stem := name
	if i := strings.IndexByte(name, '.'); i >= 0 {
		stem = name[:i]
	}
	if reserved[strings.ToUpper(stem)] {
		name = "_" + name
	}
	if len(name) > maxNameBytes {
		name = name[:maxNameBytes]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	return name
}

type sidecar struct {
	Version     int          `json:"sidecar_version"`
	Name        string       `json:"name"`
	NameCRC32   string       `json:"name_crc32"`
	NameChecked bool         `json:"name_crc32_verified"`
	Bank        string       `json:"bank"`
	Source      sidecarSrc   `json:"source"`
	Audio       sidecarAudio `json:"audio"`
	AliasOf     *string      `json:"alias_of"`
	AMTA        *sidecarAMTA `json:"amta"`
	BarsVersion string       `json:"bars_version,omitempty"`
	Tool        sidecarTool  `json:"tool"`
}

type sidecarSrc struct {
	Bars       *string `json:"bars"`
	EntryIndex *int    `json:"entry_index"`
	Stream     *string `json:"stream"`
}

type sidecarAudio struct {
	Codec          string        `json:"codec"`
	Channels       int           `json:"channels"`
	ChannelOrder   string        `json:"channel_order,omitempty"`
	SampleRate     int           `json:"sample_rate"`
	Samples        int           `json:"samples"`
	Duration       float64       `json:"duration"`
	Loop           *catalog.Loop `json:"loop"`
	PrefetchStub   bool          `json:"prefetch_stub"`
	UsedFullStream bool          `json:"used_full_stream"`
	Format         string        `json:"format"`
}

type sidecarAMTA struct {
	Version     string          `json:"version"`
	VolumeDB    float32         `json:"volume_db"`
	Channels    uint8           `json:"channels"`
	Markers     []sidecarMarker `json:"markers"`
	MinfOffset  uint32          `json:"minf_offset"`
	TagOffset   uint32          `json:"tag_offset"`
	RawDataHex  string          `json:"raw_data_hex"`
	RawExtraHex string          `json:"raw_extra_hex"`
}

type sidecarMarker struct {
	ID     uint32 `json:"id"`
	Name   string `json:"name"`
	Start  uint32 `json:"start"`
	Length uint32 `json:"length"`
}

type sidecarTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Sidecar builds the JSON describing the exported file for cue c (sidecar_version 1).
func Sidecar(c *catalog.Cue, a *render.Audio, toolVersion string) ([]byte, error) {
	s := sidecar{
		Version: 1, Name: c.Name, NameCRC32: fmt.Sprintf("0x%08x", crc32.ChecksumIEEE([]byte(c.Name))), NameChecked: c.NameVerified,
		Bank: c.Bank, BarsVersion: c.BarsVersion, Tool: sidecarTool{Name: "barsdig", Version: toolVersion},
		Audio: sidecarAudio{
			Codec: c.Codec, Channels: c.Channels, SampleRate: c.SampleRate, Samples: c.Samples, Duration: c.Duration, Loop: c.Loop,
			PrefetchStub: a.Stub, UsedFullStream: c.UsesFullStream, Format: a.Ext,
		},
	}
	if c.Channels > 2 {
		s.Audio.ChannelOrder = "bwav channel index; assumed L R C LFE BL BR (unverified)"
	}
	if c.BarsPath != "" {
		s.Source.Bars, s.Source.EntryIndex = &c.BarsPath, &c.EntryIndex
	}
	if c.StreamPath != "" {
		s.Source.Stream = &c.StreamPath
	}
	if c.AliasOf != "" {
		s.AliasOf = &c.AliasOf
	}
	if c.AMTA != nil {
		m := c.AMTA
		sa := &sidecarAMTA{
			Version: fmt.Sprintf("%d.%d", m.Version>>8, m.Version&0xFF), VolumeDB: m.VolumeDB, Channels: m.Channels,
			Markers: []sidecarMarker{}, MinfOffset: m.MinfOffset, TagOffset: m.TagOffset,
			RawDataHex: hex.EncodeToString(m.RawData), RawExtraHex: hex.EncodeToString(m.RawExtra),
		}
		for _, k := range m.Markers {
			sa.Markers = append(sa.Markers, sidecarMarker{ID: k.ID, Name: k.Name, Start: k.Start, Length: k.Length})
		}
		s.AMTA = sa
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Run exports the cues. It never stops at the first failure: each cue gets a Result.
func Run(src source.Source, cues []*catalog.Cue, opt Options) []Result {
	rd := render.New(src)
	out := filepath.Clean(opt.OutDir)
	used := map[string]bool{}
	results := make([]Result, 0, len(cues))
	for _, c := range cues {
		r := Result{CueID: c.ID}
		r.Files, r.Err = exportOne(rd, c, out, opt, used)
		results = append(results, r)
	}
	return results
}

func exportOne(rd *render.Renderer, c *catalog.Cue, out string, opt Options, used map[string]bool) ([]string, error) {
	a, err := rd.Cue(c)
	if err != nil {
		return nil, err
	}
	meta, err := Sidecar(c, a, opt.ToolVersion)
	if err != nil {
		return nil, err
	}
	parts := []string{out}
	for _, el := range strings.Split(c.Bank, "/") {
		parts = append(parts, SafeName(el))
	}
	dir := filepath.Join(parts...)
	base := SafeName(c.Name)
	name := base
	for n := 2; used[strings.ToLower(filepath.Join(dir, name))]; n++ {
		name = fmt.Sprintf("%s~%d", base, n)
	}
	used[strings.ToLower(filepath.Join(dir, name))] = true

	audioPath, metaPath := filepath.Join(dir, name+"."+a.Ext), filepath.Join(dir, name+".json")
	for _, p := range []string{audioPath, metaPath} {
		if !strings.HasPrefix(filepath.Clean(p), out+string(filepath.Separator)) {
			return nil, fmt.Errorf("export: %q would be written outside %s", p, out)
		}
		if _, err := os.Lstat(p); err == nil && !opt.Force {
			return nil, fmt.Errorf("export: %s already exists (use --force to overwrite)", p)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if opt.Force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	var written []string
	for _, f := range []struct {
		path string
		data []byte
	}{{audioPath, a.Data}, {metaPath, meta}} {
		h, err := os.OpenFile(f.path, flags, 0o644)
		if err != nil {
			return written, err
		}
		_, werr := h.Write(f.data)
		if cerr := h.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return written, errors.Join(werr, os.Remove(f.path))
		}
		written = append(written, f.path)
	}
	return written, nil
}
