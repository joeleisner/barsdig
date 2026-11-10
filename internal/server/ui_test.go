package server

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"
)

func TestIndexServedAndSelfContained(t *testing.T) {
	h, _ := handler(t)
	w := do(h, req("GET", "/", ""))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/: %d %v", w.Code, w.Header())
	}
	if !strings.Contains(w.Body.String(), `src="/static/app.js"`) {
		t.Error("index.html does not load /static/app.js")
	}
	// the page must not reach out to other hosts (no CDN scripts, fonts, images)
	for _, name := range []string{"index.html", "app.js", "style.css"} {
		b, err := fs.ReadFile(webFS(), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s := string(b); strings.Contains(s, "http://") || strings.Contains(s, "https://") || strings.Contains(s, "//cdn") {
			t.Errorf("%s references an external host", name)
		}
	}
	w = do(h, req("GET", "/static/app.js", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("/static/app.js: %d %v", w.Code, w.Header())
	}
	if w := do(h, req("GET", "/static/style.css", "")); w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "css") {
		t.Fatalf("/static/style.css: %d %v", w.Code, w.Header())
	}
}
