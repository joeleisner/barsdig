package export

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"barsdig/internal/catalog"
	"barsdig/internal/render"
	"barsdig/internal/source"
	"barsdig/internal/synth"
)

func dsp(samples, full uint32, prefetch bool) []byte {
	return synth.BWAV(synth.BWAVSpec{Codec: 1, SampleRate: 48000, FullSamples: full, Samples: samples, Prefetch: prefetch,
		Channels: [][]byte{make([]byte, 8)}})
}

func opus() []byte {
	return synth.BWAV(synth.BWAVSpec{Codec: 2, SampleRate: 48000, FullSamples: 960, Samples: 960,
		Channels: [][]byte{synth.NXOpus([][]byte{{0xF8, 0xFF, 0xFE}}, 120)}})
}

func fixture(t *testing.T, files map[string][]byte) (source.Source, *catalog.Catalog) {
	t.Helper()
	root := t.TempDir()
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src, err := source.NewDir(root)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Build(src, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return src, cat
}

func get(t *testing.T, c *catalog.Catalog, id string) *catalog.Cue {
	t.Helper()
	x, ok := c.Get(id)
	if !ok {
		t.Fatalf("no cue %q", id)
	}
	return x
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func TestLayoutBankDirWithSidecar(t *testing.T) {
	src, cat := fixture(t, map[string][]byte{
		"Sound/Resource/Obj_X.bars": synth.BARS([]synth.Entry{
			{Name: "SE_A", Channels: 1, VolumeDB: -6, BWAV: dsp(14, 14, false)},
			{Name: "Ambient_B", Channels: 1, BWAV: opus(), Markers: []synth.Marker{{ID: 1, Name: "JumpOK", Start: 5, Length: 2}}}}),
	})
	out := t.TempDir()
	res := Run(src, []*catalog.Cue{get(t, cat, "Obj_X/SE_A"), get(t, cat, "Obj_X/Ambient_B")}, Options{OutDir: out, ToolVersion: "9.9"})
	for _, r := range res {
		if r.Err != nil || len(r.Files) != 2 {
			t.Fatalf("result = %+v", r)
		}
	}
	got := files(t, out)
	want := map[string]bool{"Obj_X/SE_A.wav": true, "Obj_X/SE_A.json": true, "Obj_X/Ambient_B.opus": true, "Obj_X/Ambient_B.json": true}
	if len(got) != 4 {
		t.Fatalf("files = %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected file %s", g)
		}
	}
	b, _ := os.ReadFile(filepath.Join(out, "Obj_X", "SE_A.json"))
	var sc map[string]any
	if err := json.Unmarshal(b, &sc); err != nil {
		t.Fatal(err)
	}
	if sc["name"] != "SE_A" || sc["name_crc32"] != "0x"+strings.ToLower(hex8(synth.CRC("SE_A"))) || sc["name_crc32_verified"] != true ||
		sc["bank"] != "Obj_X" || sc["bars_version"] != "1.2" || sc["sidecar_version"] != float64(1) {
		t.Fatalf("sidecar = %s", b)
	}
	audio := sc["audio"].(map[string]any)
	if audio["codec"] != "dsp-adpcm" || audio["prefetch_stub"] != false || audio["channels"] != float64(1) || audio["sample_rate"] != float64(48000) {
		t.Fatalf("audio = %v", audio)
	}
	amta := sc["amta"].(map[string]any)
	if amta["version"] != "5.0" || amta["volume_db"] != float64(-6) || amta["raw_data_hex"] == "" {
		t.Fatalf("amta = %v", amta)
	}
	if tool := sc["tool"].(map[string]any); tool["name"] != "barsdig" || tool["version"] != "9.9" {
		t.Fatalf("tool = %v", tool)
	}
	b, _ = os.ReadFile(filepath.Join(out, "Obj_X", "Ambient_B.json"))
	json.Unmarshal(b, &sc)
	if m := sc["amta"].(map[string]any)["markers"].([]any); len(m) != 1 || m[0].(map[string]any)["name"] != "JumpOK" {
		t.Fatalf("markers = %v", sc["amta"])
	}
}

func hex8(v uint32) string {
	const d = "0123456789abcdef"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = d[v&0xF]
		v >>= 4
	}
	return string(b)
}

func TestStubExportedAsStubSaysSo(t *testing.T) {
	src, cat := fixture(t, map[string][]byte{
		"Sound/Resource/B.bars": synth.BARS([]synth.Entry{{Name: "Ambient_A", Channels: 1, BWAV: dsp(14, 28, true)}}),
	})
	out := t.TempDir()
	if r := Run(src, []*catalog.Cue{get(t, cat, "B/Ambient_A")}, Options{OutDir: out}); r[0].Err != nil {
		t.Fatal(r[0].Err)
	}
	b, _ := os.ReadFile(filepath.Join(out, "B", "Ambient_A.json"))
	var m map[string]map[string]any
	json.Unmarshal(b, &m)
	if m["audio"]["prefetch_stub"] != true || m["audio"]["used_full_stream"] != false {
		t.Fatalf("audio = %v", m["audio"])
	}
}

func TestNeverWritesBWAV(t *testing.T) {
	src, cat := fixture(t, map[string][]byte{
		"Sound/Resource/Stream/S.bwav": dsp(14, 14, false),
		"Sound/Resource/Stream/O.bwav": opus(),
	})
	out := t.TempDir()
	Run(src, cat.Cues, Options{OutDir: out})
	for _, f := range files(t, out) {
		if strings.HasSuffix(f, ".bwav") {
			t.Errorf("exported %s", f)
		}
	}
	if len(files(t, out)) != 4 {
		t.Fatalf("files = %v", files(t, out))
	}
}

func TestNoOverwriteWithoutForce(t *testing.T) {
	src, cat := fixture(t, map[string][]byte{"Sound/Resource/B.bars": synth.BARS([]synth.Entry{{Name: "SE_A", Channels: 1, BWAV: dsp(14, 14, false)}})})
	out := t.TempDir()
	os.MkdirAll(filepath.Join(out, "B"), 0o755)
	existing := filepath.Join(out, "B", "SE_A.wav")
	os.WriteFile(existing, []byte("mine"), 0o644)
	cues := []*catalog.Cue{get(t, cat, "B/SE_A")}
	if r := Run(src, cues, Options{OutDir: out}); r[0].Err == nil {
		t.Fatal("overwrote without --force")
	}
	if b, _ := os.ReadFile(existing); string(b) != "mine" {
		t.Fatal("existing file modified")
	}
	if _, err := os.Stat(filepath.Join(out, "B", "SE_A.json")); err == nil {
		t.Fatal("sidecar written although the audio was refused")
	}
	if r := Run(src, cues, Options{OutDir: out, Force: true}); r[0].Err != nil {
		t.Fatal(r[0].Err)
	}
	if b, _ := os.ReadFile(existing); string(b) == "mine" {
		t.Fatal("--force did not replace the file")
	}
}

func TestNoAudioCueReported(t *testing.T) {
	src, cat := fixture(t, map[string][]byte{"Sound/Resource/B.bars": synth.BARS([]synth.Entry{{Name: "SE_Silent", Channels: 1}})})
	out := t.TempDir()
	r := Run(src, []*catalog.Cue{get(t, cat, "B/SE_Silent")}, Options{OutDir: out})
	if !errors.Is(r[0].Err, render.ErrNoAudio) || len(r[0].Files) != 0 || len(files(t, out)) != 0 {
		t.Fatalf("result = %+v files=%v", r[0], files(t, out))
	}
}

func TestSafeName(t *testing.T) {
	long := strings.Repeat("x", 500)
	cases := map[string]string{
		"SE_Box_A_Open_00":     "SE_Box_A_Open_00",
		"Seq_Name-with.dots.1": "Seq_Name-with.dots.1",
		"../x":                 ".._x",
		"a/b":                  "a_b",
		`a\b`:                  "a_b",
		"a\x00b":               "a_b",
		"a:b*c?d":              "a_b_c_d",
		"CON":                  "_CON",
		"nul.txt":              "_nul.txt",
		"name. ":               "name",
		"":                     "_",
		".":                    "_",
		"..":                   "__",
	}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SafeName(long); len(got) > 200 || got == "" {
		t.Errorf("long name -> %d bytes", len(got))
	}
}

func TestCollisionsGetSuffixAndNothingEscapesOut(t *testing.T) {
	entries := []synth.Entry{
		{Name: "a/b", Channels: 1, BWAV: dsp(14, 14, false)},
		{Name: "a_b", Channels: 1, BWAV: dsp(14, 14, false)},
		{Name: "A_B", Channels: 1, BWAV: dsp(14, 14, false)},
		{Name: "../../escape", Channels: 1, BWAV: dsp(14, 14, false)},
		{Name: `..\..\evil`, Channels: 1, BWAV: dsp(14, 14, false)},
	}
	src, cat := fixture(t, map[string][]byte{"Sound/Resource/B.bars": synth.BARS(entries)})
	parent := t.TempDir()
	out := filepath.Join(parent, "out")
	res := Run(src, cat.Cues, Options{OutDir: out})
	if len(res) != 5 {
		t.Fatalf("%d results", len(res))
	}
	seen := map[string]bool{}
	for _, r := range res {
		if r.Err != nil {
			t.Fatalf("result %+v", r)
		}
		for _, f := range r.Files {
			if !strings.HasPrefix(filepath.Clean(f), filepath.Clean(out)+string(filepath.Separator)) {
				t.Errorf("%s escapes %s", f, out)
			}
			if seen[strings.ToLower(f)] {
				t.Errorf("collision: %s written twice", f)
			}
			seen[strings.ToLower(f)] = true
		}
	}
	if got := files(t, parent); len(got) != 10 {
		t.Fatalf("expected 10 files under out/ and none beside it, got %v", got)
	}
}

func TestBankWithSubdirectories(t *testing.T) {
	src, cat := fixture(t, map[string][]byte{"Voice/Resource/Stream/V.bwav": dsp(14, 14, false)})
	out := t.TempDir()
	Run(src, cat.Cues, Options{OutDir: out})
	if got := files(t, out); len(got) != 2 || got[0] != "Voice/Stream/V.json" {
		t.Fatalf("files = %v", got)
	}
}
