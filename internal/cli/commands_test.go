package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"barsdig/internal/synth"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func dsp() []byte {
	return synth.BWAV(synth.BWAVSpec{Codec: 1, SampleRate: 48000, FullSamples: 14, Samples: 14, Channels: [][]byte{make([]byte, 8)}})
}

func zst(t *testing.T, b []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	return enc.EncodeAll(b, nil)
}

// romfs builds a small RomFS with a compressed bank and a standalone stream.
func romfs(t *testing.T, extra map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	files := map[string][]byte{
		"Sound/Resource/Obj_X.bars.zs": zst(t, synth.BARS([]synth.Entry{
			{Name: "SE_A", Channels: 1, BWAV: dsp()}, {Name: "SE_B", Channels: 1, BWAV: dsp()}})),
		"Sound/Resource/Stream/Foo.bwav": dsp(),
	}
	for k, v := range extra {
		files[k] = v
	}
	for rel, d := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, d, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runCLI(t *testing.T, args ...string) (code int, out, errs string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(context.Background(), args, &o, &e)
	return code, o.String(), e.String()
}

func TestIndexSummary(t *testing.T) {
	root, cache := romfs(t, nil), t.TempDir()
	code, out, errs := runCLI(t, "index", root, "--cache-dir", cache)
	if code != 0 {
		t.Fatalf("code %d, out %q err %q", code, out, errs)
	}
	for _, want := range []string{"Cues: 3", "dsp-adpcm 3", "Problems: 0", "Banks: 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	_, out2, _ := runCLI(t, "index", root, "--cache-dir", cache)
	if !strings.Contains(out2, "(cached)") {
		t.Errorf("second run not served from cache:\n%s", out2)
	}
}

func TestIndexExitsNonZeroOnProblem(t *testing.T) {
	root := romfs(t, map[string][]byte{"Sound/Resource/Stream/junk.bwav": []byte("not a bwav")})
	code, out, _ := runCLI(t, "index", root, "--cache-dir", "")
	if code != 1 || !strings.Contains(out, "Problems: 1") || !strings.Contains(out, "junk.bwav") {
		t.Fatalf("code %d:\n%s", code, out)
	}
}

func TestIndexUsage(t *testing.T) {
	if code, _, errs := runCLI(t, "index"); code != 2 || !strings.Contains(errs, "usage") {
		t.Fatalf("code %d, %q", code, errs)
	}
	if code, _, _ := runCLI(t, "index", filepath.Join(t.TempDir(), "missing")); code != 1 {
		t.Fatalf("missing romfs: code %d", code)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestExportByMatchAndBank(t *testing.T) {
	root, out := romfs(t, nil), t.TempDir()
	code, stdout, errs := runCLI(t, "export", root, "--out", out, "--match", "se_a", "--cache-dir", "")
	if code != 0 {
		t.Fatalf("code %d %q %q", code, stdout, errs)
	}
	if !exists(filepath.Join(out, "Obj_X", "SE_A.wav")) || !exists(filepath.Join(out, "Obj_X", "SE_A.json")) || exists(filepath.Join(out, "Obj_X", "SE_B.wav")) {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
	out2 := t.TempDir()
	if code, _, e := runCLI(t, "export", root, "--out", out2, "--bank", "Obj_X", "--cache-dir", ""); code != 0 || !exists(filepath.Join(out2, "Obj_X", "SE_B.wav")) {
		t.Fatalf("bank export: code %d %q", code, e)
	}
	if code, _, _ := runCLI(t, "export", root, "--out", out, "--match", "se_a", "--cache-dir", ""); code != 1 {
		t.Errorf("re-export without --force should fail, got %d", code)
	}
	if code, _, _ := runCLI(t, "export", root, "--out", out, "--match", "se_a", "--force", "--cache-dir", ""); code != 0 {
		t.Errorf("--force re-export failed: %d", code)
	}
}

func TestExportFromPicksFile(t *testing.T) {
	root, out := romfs(t, nil), t.TempDir()
	picks := filepath.Join(t.TempDir(), "picks.txt")
	os.WriteFile(picks, []byte("# my picks\n\nObj_X/SE_B\n  Stream/Foo  \nNope/Missing\n"), 0o644)
	code, stdout, _ := runCLI(t, "export", root, "--out", out, "--picks", picks, "--cache-dir", "")
	if code != 1 || !strings.Contains(stdout, "Nope/Missing") {
		t.Fatalf("unknown id should fail the run and be named: code %d\n%s", code, stdout)
	}
	if !exists(filepath.Join(out, "Obj_X", "SE_B.wav")) || !exists(filepath.Join(out, "Stream", "Foo.wav")) || exists(filepath.Join(out, "Obj_X", "SE_A.wav")) {
		t.Fatalf("wrong files exported:\n%s", stdout)
	}
}

func TestExportNeedsOutAndSelection(t *testing.T) {
	root := romfs(t, nil)
	if code, _, e := runCLI(t, "export", root, "--match", "x", "--cache-dir", ""); code != 2 || !strings.Contains(e, "--out") {
		t.Errorf("no --out: %d %q", code, e)
	}
	if code, _, e := runCLI(t, "export", root, "--out", t.TempDir(), "--cache-dir", ""); code != 2 || !strings.Contains(e, "--picks") {
		t.Errorf("no selection: %d %q", code, e)
	}
}

func TestServeRefusesNonLoopback(t *testing.T) {
	root := romfs(t, nil)
	for _, addr := range []string{"0.0.0.0:0", ":0", "192.168.1.10:0"} {
		code, _, errs := runCLI(t, "serve", root, "--addr", addr, "--no-open", "--cache-dir", "")
		if code != 2 || !strings.Contains(errs, "non-loopback") {
			t.Errorf("%s: code %d %q", addr, code, errs)
		}
	}
}

func TestServeStartsAndAnswers(t *testing.T) {
	root := romfs(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errb syncBuf
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"serve", root, "--addr", "127.0.0.1:0", "--no-open", "--cache-dir", "", "--out", t.TempDir()}, &out, &errb)
	}()
	re := regexp.MustCompile(`http://127\.0\.0\.1:\d+/`)
	var url string
	for i := 0; i < 200 && url == ""; i++ {
		url = re.FindString(out.String())
		time.Sleep(10 * time.Millisecond)
	}
	if url == "" {
		t.Fatalf("no URL printed; stdout %q stderr %q", out.String(), errb.String())
	}
	resp, err := http.Get(url + "api/facets")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var f struct{ Banks []string }
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&f) != nil || len(f.Banks) != 2 {
		t.Fatalf("facets: %d %+v", resp.StatusCode, f)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop after cancel")
	}
}
