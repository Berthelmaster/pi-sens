package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const indexHTML = "<!doctype html><title>pisens</title>"

var testUI = fstest.MapFS{
	".gitkeep":            {},
	"index.html":          {Data: []byte(indexHTML)},
	"favicon.svg":         {Data: []byte("<svg></svg>")},
	"assets/index-abc.js": {Data: []byte("console.log(1)")},
}

func get(t *testing.T, s *Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestUIServesIndexAtRoot(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)

	for _, target := range []string{"/", "/index.html"} {
		rec := get(t, s, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want %d", target, rec.Code, http.StatusOK)
		}
		if body := rec.Body.String(); body != indexHTML {
			t.Errorf("GET %s: body = %q, want index.html", target, body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s: Content-Type = %q, want text/html", target, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != cacheRevalidate {
			t.Errorf("GET %s: Cache-Control = %q, want %q", target, cc, cacheRevalidate)
		}
	}
}

func TestUIDeepLinksServeIndex(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)

	for _, target := range []string{"/history", "/history/", "/a/b/c", "/assets"} {
		rec := get(t, s, target)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want %d", target, rec.Code, http.StatusOK)
			continue
		}
		if body := rec.Body.String(); body != indexHTML {
			t.Errorf("GET %s: body = %q, want index.html", target, body)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != cacheRevalidate {
			t.Errorf("GET %s: Cache-Control = %q, want %q", target, cc, cacheRevalidate)
		}
	}
}

func TestUIServesStaticFiles(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)

	tests := []struct {
		target, body, cache, contentType string
	}{
		{"/assets/index-abc.js", "console.log(1)", cacheImmutable, "text/javascript"},
		{"/favicon.svg", "<svg></svg>", cacheRevalidate, "image/svg+xml"},
	}
	for _, tt := range tests {
		rec := get(t, s, tt.target)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want %d", tt.target, rec.Code, http.StatusOK)
			continue
		}
		if body := rec.Body.String(); body != tt.body {
			t.Errorf("GET %s: body = %q, want %q", tt.target, body, tt.body)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != tt.cache {
			t.Errorf("GET %s: Cache-Control = %q, want %q", tt.target, cc, tt.cache)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tt.contentType) {
			t.Errorf("GET %s: Content-Type = %q, want %s", tt.target, ct, tt.contentType)
		}
	}
}

func TestUIMissingFilesAre404(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)

	for _, target := range []string{"/assets/missing.js", "/robots.txt", "/.gitkeep"} {
		if rec := get(t, s, target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d", target, rec.Code, http.StatusNotFound)
		}
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)

	for _, target := range []string{"/api", "/api/", "/api/nope"} {
		rec := get(t, s, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d", target, rec.Code, http.StatusNotFound)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("GET %s: Content-Type = %q, want application/json", target, ct)
		}
	}
}

func TestUINotBuilt(t *testing.T) {
	s := New(":0", "dev", fstest.MapFS{".gitkeep": {}}, discardLogger(), &fakeSource{}, nil, nil)

	if rec := get(t, s, "/"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /: status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestUIRejectsOtherMethods(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/history", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /history: status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
