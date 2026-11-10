package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"barsdig/internal/catalog"
	"barsdig/internal/render"
	"barsdig/internal/source"
	"barsdig/internal/synth"
)

func dsp(samples uint32, frames int) []byte {
	return synth.BWAV(synth.BWAVSpec{Codec: 1, SampleRate: 48000, FullSamples: samples, Samples: samples, Channels: [][]byte{make([]byte, 8*frames)}})
}

func opus() []byte {
	return synth.BWAV(synth.BWAVSpec{Codec: 2, SampleRate: 48000, FullSamples: 960, Samples: 960,
		Channels: [][]byte{synth.NXOpus([][]byte{{0xF8, 0xFF, 0xFE}}, 120)}})
}

func fixture(t *testing.T) (*catalog.Catalog, source.Source) {
	t.Helper()
	root := t.TempDir()
	files := map[string][]byte{
		"Sound/Resource/Obj_Widget.bars": synth.BARS([]synth.Entry{
			{Name: "SE_Widget_Appear", Channels: 1, BWAV: dsp(28, 2)},
			{Name: "SE_Widget_Land", Channels: 1, BWAV: dsp(14, 1)},
			{Name: "SE_Silent", Channels: 1},
		}),
		"Sound/Resource/Stream/Ambient_Field.bwav": opus(),
		"Sound/Resource/Stream/Weird.bwav": synth.BWAV(synth.BWAVSpec{Codec: 9, SampleRate: 48000, FullSamples: 14, Samples: 14,
			Channels: [][]byte{make([]byte, 8)}}),
	}
	for rel, d := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, d, 0o644)
	}
	src, err := source.NewDir(root)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Build(src, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return cat, src
}

func handler(t *testing.T) (http.Handler, string) {
	cat, src := fixture(t)
	out := t.TempDir()
	return New(cat, src, Options{OutDir: out, ToolVersion: "t"}), out
}

func req(method, target string, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+target, strings.NewReader(body))
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func do(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCuesSearchAndPaging(t *testing.T) {
	h, _ := handler(t)
	var res struct {
		Total int
		Cues  []struct{ ID, Name, Bank, Codec string }
	}
	w := do(h, req("GET", "/api/cues?q=se_widget&sort=name&desc=1", ""))
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &res) != nil || res.Total != 2 || res.Cues[0].Name != "SE_Widget_Land" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = do(h, req("GET", "/api/cues?limit=1&offset=1", ""))
	json.Unmarshal(w.Body.Bytes(), &res)
	if res.Total != 5 || len(res.Cues) != 1 {
		t.Fatalf("paging: total %d, %d cues", res.Total, len(res.Cues))
	}
	w = do(h, req("GET", "/api/cues?codec=opus", ""))
	json.Unmarshal(w.Body.Bytes(), &res)
	if res.Total != 1 || res.Cues[0].Name != "Ambient_Field" {
		t.Fatalf("codec filter: %s", w.Body)
	}
	if w := do(h, req("GET", "/api/cues?limit=abc", "")); w.Code != 400 {
		t.Errorf("bad limit: %d", w.Code)
	}
	if w := do(h, req("GET", "/api/cues?limit=999999", "")); w.Code != 200 {
		t.Errorf("huge limit should be clamped, got %d", w.Code)
	}
}

func TestFacetsAndProblems(t *testing.T) {
	h, _ := handler(t)
	var f catalog.Facets
	w := do(h, req("GET", "/api/facets", ""))
	if json.Unmarshal(w.Body.Bytes(), &f) != nil || len(f.Banks) != 2 {
		t.Fatalf("facets: %s", w.Body)
	}
	w = do(h, req("GET", "/api/problems", ""))
	if w.Code != 200 || !strings.HasPrefix(strings.TrimSpace(w.Body.String()), "[") {
		t.Fatalf("problems: %d %s", w.Code, w.Body)
	}
}

func TestAudioWAVWithRange(t *testing.T) {
	h, _ := handler(t)
	id := url.PathEscape("Obj_Widget/SE_Widget_Appear")
	w := do(h, req("GET", "/audio/"+id, ""))
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/wav" || w.Header().Get("Accept-Ranges") != "bytes" || !strings.HasPrefix(w.Body.String(), "RIFF") {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	total := w.Body.Len()
	r := req("GET", "/audio/"+id, "")
	r.Header.Set("Range", "bytes=0-9")
	w = do(h, r)
	if w.Code != 206 || w.Body.Len() != 10 || w.Header().Get("Content-Range") != "bytes 0-9/"+itoa(total) {
		t.Fatalf("range: %d %v len %d", w.Code, w.Header(), w.Body.Len())
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestAudioOpusContentType(t *testing.T) {
	h, _ := handler(t)
	w := do(h, req("GET", "/audio/"+url.PathEscape("Stream/Ambient_Field"), ""))
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/ogg" || !strings.HasPrefix(w.Body.String(), "OggS") {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}

func TestAudioErrors(t *testing.T) {
	h, _ := handler(t)
	for path, want := range map[string]int{
		"/audio/" + url.PathEscape("Nope/Nope"):            404,
		"/audio/" + url.PathEscape("Obj_Widget/SE_Silent"): 409,
		"/audio/" + url.PathEscape("Stream/Weird"):         415,
		"/audio/" + url.PathEscape("../../etc/passwd"):     404,
	} {
		if w := do(h, req("GET", path, "")); w.Code != want {
			t.Errorf("%s: %d, want %d (%s)", path, w.Code, want, w.Body)
		}
	}
}

func TestLargeAudioSeekDoesNotRerender(t *testing.T) {
	cat, src := fixture(t)
	rd := render.New(src)
	var calls atomic.Int32
	h := newServer(cat, func(c *catalog.Cue) (*render.Audio, error) { calls.Add(1); return rd.Cue(c) }, nil, Options{OutDir: t.TempDir()})
	id := url.PathEscape("Obj_Widget/SE_Widget_Appear")
	do(h, req("GET", "/audio/"+id, ""))
	for i := 0; i < 3; i++ {
		r := req("GET", "/audio/"+id, "")
		r.Header.Set("Range", "bytes=10-19")
		if w := do(h, r); w.Code != 206 {
			t.Fatalf("range %d: %d", i, w.Code)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("rendered %d times, want 1", calls.Load())
	}
}

func TestExportWritesFiles(t *testing.T) {
	h, out := handler(t)
	w := do(h, req("POST", "/api/export", `{"ids":["Obj_Widget/SE_Widget_Land","Stream/Ambient_Field"]}`))
	var res struct {
		OutDir  string `json:"out_dir"`
		Results []struct {
			ID    string
			Files []string
			Error string
		}
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &res) != nil || len(res.Results) != 2 || res.OutDir != out {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, r := range res.Results {
		if r.Error != "" || len(r.Files) != 2 {
			t.Errorf("result %+v", r)
		}
	}
	for _, f := range []string{"Obj_Widget/SE_Widget_Land.wav", "Obj_Widget/SE_Widget_Land.json", "Stream/Ambient_Field.opus"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
}

func TestExportRejectsUnknownIDsAndNoPaths(t *testing.T) {
	h, out := handler(t)
	w := do(h, req("POST", "/api/export", `{"ids":["../../etc/passwd","Obj_Widget/../../x","Nope/Nope"]}`))
	var res struct{ Results []struct{ ID, Error string } }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &res) != nil || len(res.Results) != 3 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, r := range res.Results {
		if r.Error == "" {
			t.Errorf("%q exported", r.ID)
		}
	}
	if w := do(h, req("POST", "/api/export", `{"ids":[],"path":"/tmp/evil"}`)); w.Code != 400 {
		t.Errorf("unknown field accepted: %d", w.Code)
	}
	if w := do(h, req("POST", "/api/export", `not json`)); w.Code != 400 {
		t.Errorf("bad json: %d", w.Code)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("files written for rejected requests: %v", entries)
	}
}

func TestExportRequiresJSONContentType(t *testing.T) {
	h, _ := handler(t)
	r := req("POST", "/api/export", `{"ids":[]}`)
	r.Header.Set("Content-Type", "text/plain")
	if w := do(h, r); w.Code != 415 {
		t.Fatalf("text/plain accepted: %d", w.Code)
	}
}

func TestForeignHostAndOriginRejected(t *testing.T) {
	h, _ := handler(t)
	r := req("GET", "/api/facets", "")
	r.Host = "evil.example:8080"
	if w := do(h, r); w.Code != 403 {
		t.Errorf("foreign Host: %d", w.Code)
	}
	r = req("POST", "/api/export", `{"ids":[]}`)
	r.Header.Set("Origin", "http://evil.example")
	if w := do(h, r); w.Code != 403 {
		t.Errorf("foreign Origin: %d", w.Code)
	}
	r = req("GET", "/api/facets", "")
	r.Host = "localhost:8080"
	r.Header.Set("Origin", "http://localhost:8080")
	if w := do(h, r); w.Code != 200 {
		t.Errorf("localhost rejected: %d", w.Code)
	}
}

func TestNonLoopbackHostAllowedWhenConfigured(t *testing.T) {
	cat, src := fixture(t)
	h := New(cat, src, Options{OutDir: t.TempDir(), AnyHost: true})
	r := req("GET", "/api/facets", "")
	r.Host = "192.168.1.5:8080"
	if w := do(h, r); w.Code != 200 {
		t.Fatalf("AnyHost: %d", w.Code)
	}
}

func TestNoCORSHeaders(t *testing.T) {
	h, _ := handler(t)
	w := do(h, req("GET", "/api/facets", ""))
	for k := range w.Header() {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("CORS header %s", k)
		}
	}
	if w := do(h, req("OPTIONS", "/api/export", "")); w.Code != 405 {
		t.Errorf("OPTIONS: %d", w.Code)
	}
}

func TestIndexServed(t *testing.T) {
	h, _ := handler(t)
	w := do(h, req("GET", "/", ""))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}

func TestConcurrentRequestsRenderOnce(t *testing.T) {
	cat, src := fixture(t)
	rd := render.New(src)
	var calls atomic.Int32
	h := newServer(cat, func(c *catalog.Cue) (*render.Audio, error) {
		calls.Add(1)
		time.Sleep(80 * time.Millisecond) // a slow render: concurrent requests pile up behind it
		return rd.Cue(c)
	}, nil, Options{OutDir: t.TempDir()})
	id := url.PathEscape("Obj_Widget/SE_Widget_Appear")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := do(h, req("GET", "/audio/"+id, "")); w.Code != 200 {
				t.Errorf("status %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("rendered %d times for 8 concurrent requests, want 1", calls.Load())
	}
}

func TestCueByID(t *testing.T) {
	h, _ := handler(t)
	var c struct {
		ID       string
		Name     string
		Codec    string
		Duration float64
	}
	w := do(h, req("GET", "/api/cue?id="+url.QueryEscape("Obj_Widget/SE_Widget_Appear"), ""))
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &c) != nil || c.ID != "Obj_Widget/SE_Widget_Appear" || c.Codec != "dsp-adpcm" || c.Duration == 0 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do(h, req("GET", "/api/cue?id=Nope%2FNope", "")); w.Code != 404 {
		t.Errorf("unknown id: %d", w.Code)
	}
	if w := do(h, req("GET", "/api/cue", "")); w.Code != 400 {
		t.Errorf("missing id: %d", w.Code)
	}
}
