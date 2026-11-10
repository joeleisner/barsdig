// Package server exposes the catalog over a small local HTTP API and serves
// the embedded web UI.
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"barsdig/internal/bwav"
	"barsdig/internal/catalog"
	"barsdig/internal/export"
	"barsdig/internal/render"
	"barsdig/internal/source"
)

// Options configure the server.
type Options struct {
	OutDir      string // where /api/export writes
	ToolVersion string
	AnyHost     bool // accept non-loopback Host headers (--allow-nonloopback)
}

const (
	defaultLimit  = 200
	maxLimit      = 1000
	maxExportIDs  = 20000
	maxBodyBytes  = 1 << 20
	audioCacheMax = 512 << 20
)

type renderFunc func(*catalog.Cue) (*render.Audio, error)
type exportFunc func([]*catalog.Cue) []export.Result

// New returns the HTTP handler for a catalog.
func New(cat *catalog.Catalog, src source.Source, opt Options) http.Handler {
	rd := render.New(src)
	return newServer(cat, rd.Cue, func(cues []*catalog.Cue) []export.Result {
		return export.Run(src, cues, export.Options{OutDir: opt.OutDir, ToolVersion: opt.ToolVersion})
	}, opt)
}

type flight struct {
	done chan struct{}
	v    *cached
	err  error
}

// load returns the rendered cue from the cache, rendering it once even when
// several requests (a browser issues many Range requests) arrive together.
func (s *server) load(id string, cue *catalog.Cue) (*cached, error) {
	if v, ok := s.cache.get(id); ok {
		return v, nil
	}
	s.flightMu.Lock()
	if f, ok := s.flights[id]; ok {
		s.flightMu.Unlock()
		<-f.done
		return f.v, f.err
	}
	f := &flight{done: make(chan struct{})}
	s.flights[id] = f
	s.flightMu.Unlock()

	if a, err := s.render(cue); err != nil {
		f.err = err
	} else {
		f.v = &cached{data: a.Data, contentType: a.ContentType, stub: a.Stub}
		s.cache.put(id, f.v)
	}
	s.flightMu.Lock()
	delete(s.flights, id)
	s.flightMu.Unlock()
	close(f.done)
	return f.v, f.err
}

type server struct {
	cat      *catalog.Catalog
	render   renderFunc
	export   exportFunc
	opt      Options
	cache    *audioCache
	exportMu sync.Mutex
	flightMu sync.Mutex
	flights  map[string]*flight
}

func newServer(cat *catalog.Catalog, rf renderFunc, ef exportFunc, opt Options) http.Handler {
	s := &server{cat: cat, render: rf, export: ef, opt: opt, cache: newAudioCache(audioCacheMax), flights: map[string]*flight{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/cues", s.cues)
	mux.HandleFunc("GET /api/facets", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, cat.Facets()) })
	mux.HandleFunc("GET /api/problems", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, cat.Problems) })
	mux.HandleFunc("GET /api/cue", s.cue)
	mux.HandleFunc("GET /audio/{id}", s.audio)
	mux.HandleFunc("POST /api/export", s.exportCues)
	static := http.FileServerFS(webFS())
	mux.Handle("GET /static/", http.StripPrefix("/static/", static))
	mux.Handle("GET /{$}", static)
	return s.guard(mux)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, format string, a ...any) {
	writeJSON(w, code, map[string]string{"error": fmt.Sprintf(format, a...)})
}

// guard rejects requests whose Host is not loopback (DNS rebinding) and
// cross-origin requests; it adds no CORS headers.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.opt.AnyHost && !loopbackHost(r.Host) {
			writeErr(w, http.StatusForbidden, "host %q not allowed", r.Host)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Host != r.Host {
				writeErr(w, http.StatusForbidden, "cross-origin request rejected")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(hostport string) bool {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

type apiCue struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Bank           string        `json:"bank"`
	Codec          string        `json:"codec"`
	Channels       int           `json:"channels"`
	SampleRate     int           `json:"sample_rate"`
	Samples        int           `json:"samples"`
	Duration       float64       `json:"duration"`
	Loop           *catalog.Loop `json:"loop,omitempty"`
	PrefetchStub   bool          `json:"prefetch_stub"`
	UsesFullStream bool          `json:"uses_full_stream"`
	NoAudio        bool          `json:"no_audio"`
	AliasOf        string        `json:"alias_of,omitempty"`
	NameVerified   bool          `json:"name_verified"`
}

func toAPI(c *catalog.Cue) apiCue {
	return apiCue{c.ID, c.Name, c.Bank, c.Codec, c.Channels, c.SampleRate, c.Samples, c.Duration, c.Loop,
		c.PrefetchStub, c.UsesFullStream, c.NoAudio, c.AliasOf, c.NameVerified}
}

// cue returns one cue by ID (?id=), for clients that only remember IDs.
func (s *server) cue(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing id")
		return
	}
	c, ok := s.cat.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown cue")
		return
	}
	writeJSON(w, 200, toAPI(c))
}

func (s *server) cues(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query()
	q := catalog.Query{Text: p.Get("q"), Banks: p["bank"], Codecs: p["codec"], Sort: p.Get("sort"),
		LoopOnly: p.Get("loop") == "1", Desc: p.Get("desc") == "1"}
	var err error
	num := func(name string, dst *float64) {
		if v := p.Get(name); v != "" && err == nil {
			*dst, err = strconv.ParseFloat(v, 64)
		}
	}
	integer := func(name string, dst *int) {
		if v := p.Get(name); v != "" && err == nil {
			*dst, err = strconv.Atoi(v)
		}
	}
	limit, offset := defaultLimit, 0
	num("min", &q.MinSec)
	num("max", &q.MaxSec)
	integer("ch", &q.Channels)
	integer("limit", &limit)
	integer("offset", &offset)
	if err != nil || limit < 0 || offset < 0 {
		writeErr(w, http.StatusBadRequest, "bad query parameter")
		return
	}
	limit = min(limit, maxLimit)
	all := s.cat.Search(q)
	page := all[min(offset, len(all)):]
	page = page[:min(limit, len(page))]
	out := make([]apiCue, 0, len(page))
	for _, c := range page {
		out = append(out, toAPI(c))
	}
	writeJSON(w, 200, map[string]any{"total": len(all), "cues": out})
}

func (s *server) audio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cue, ok := s.cat.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown cue")
		return
	}
	v, err := s.load(id, cue)
	var uc *bwav.UnsupportedCodecError
	switch {
	case errors.Is(err, render.ErrNoAudio):
		writeErr(w, http.StatusConflict, "this cue has no audio")
		return
	case errors.As(err, &uc):
		writeErr(w, http.StatusUnsupportedMediaType, "unsupported codec %d", uc.Codec)
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", v.contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	if v.stub {
		h.Set("X-Barsdig-Stub", "1")
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(v.data))
}

type exportResult struct {
	ID    string   `json:"id"`
	Files []string `json:"files"`
	Error string   `json:"error,omitempty"`
}

func (s *server) exportCues(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeErr(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	if s.export == nil {
		writeErr(w, http.StatusNotImplemented, "export is not configured")
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || len(body.IDs) > maxExportIDs {
		writeErr(w, http.StatusBadRequest, "bad request body")
		return
	}
	results := make([]exportResult, len(body.IDs))
	var cues []*catalog.Cue
	var at []int
	for i, id := range body.IDs {
		results[i] = exportResult{ID: id, Files: []string{}}
		if c, ok := s.cat.Get(id); ok {
			cues, at = append(cues, c), append(at, i)
		} else {
			results[i].Error = "unknown cue"
		}
	}
	s.exportMu.Lock()
	res := s.export(cues)
	s.exportMu.Unlock()
	for k, rr := range res {
		i := at[k]
		if rr.Files != nil {
			results[i].Files = rr.Files
		}
		if rr.Err != nil {
			results[i].Error = rr.Err.Error()
		}
	}
	writeJSON(w, 200, map[string]any{"out_dir": s.opt.OutDir, "results": results})
}
