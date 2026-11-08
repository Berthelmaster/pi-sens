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
	".gitkeep":             {},
	"index.html":           {Data: []byte(indexHTML)},
	"favicon.svg":          {Data: []byte("<svg></svg>")},
	"manifest.webmanifest": {Data: []byte(`{"name":"pisens"}`)},
	"icons/icon-192.png":   {Data: []byte("\x89PNG\r\n\x1a\n")},
	"assets/index-abc.js":  {Data: []byte("console.log(1)")},
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
		{"/icons/icon-192.png", "\x89PNG\r\n\x1a\n", cacheRevalidate, "image/png"},
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

// Browsers want the web app manifest as application/manifest+json. Go's mime
// package has no entry for .webmanifest, so without an explicit type it would
// be sniffed as text/plain.
func TestUIServesManifestAsManifestJSON(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)

	rec := get(t, s, "/manifest.webmanifest")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest: status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("Content-Type = %q, want application/manifest+json", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != cacheRevalidate {
		t.Errorf("Cache-Control = %q, want %q", cc, cacheRevalidate)
	}
	if body := rec.Body.String(); body != `{"name":"pisens"}` {
		t.Errorf("body = %q, want the manifest", body)
	}

	// It revalidates like every other file.
	etag := rec.Header().Get("ETag")
	if rec := getWithETag(t, s, "/manifest.webmanifest", etag); rec.Code != http.StatusNotModified {
		t.Errorf("GET with matching If-None-Match: status = %d, want %d", rec.Code, http.StatusNotModified)
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

func getWithETag(t *testing.T, s *Server, target, etag string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestUIFilesHaveStrongETags(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)

	seen := map[string]string{}
	for _, target := range []string{"/", "/favicon.svg", "/assets/index-abc.js"} {
		etag := get(t, s, target).Header().Get("ETag")
		if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || len(etag) < 3 {
			t.Errorf("GET %s: ETag = %q, want a strong quoted ETag", target, etag)
		}
		if other, ok := seen[etag]; ok {
			t.Errorf("GET %s: ETag %q is the same as for %s", target, etag, other)
		}
		seen[etag] = target
	}
}

func TestUIMatchingIfNoneMatchIs304(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)

	for _, target := range []string{"/", "/index.html", "/favicon.svg", "/assets/index-abc.js"} {
		etag := get(t, s, target).Header().Get("ETag")

		rec := getWithETag(t, s, target, etag)
		if rec.Code != http.StatusNotModified {
			t.Errorf("GET %s with matching If-None-Match: status = %d, want %d", target, rec.Code, http.StatusNotModified)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("GET %s with matching If-None-Match: body = %q, want empty", target, rec.Body.String())
		}
		if got := rec.Header().Get("ETag"); got != etag {
			t.Errorf("GET %s with matching If-None-Match: ETag = %q, want %q", target, got, etag)
		}

		rec = getWithETag(t, s, target, `"stale"`)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s with stale If-None-Match: status = %d, want %d", target, rec.Code, http.StatusOK)
		}
	}
}

func TestUIETagFollowsContent(t *testing.T) {
	etagOf := func(index string) string {
		ui := fstest.MapFS{"index.html": {Data: []byte(index)}}
		s := New(":0", "dev", ui, discardLogger(), &fakeSource{}, nil, nil)
		return get(t, s, "/").Header().Get("ETag")
	}

	before := etagOf(indexHTML)
	if again := etagOf(indexHTML); again != before {
		t.Errorf("same content: ETag = %q, then %q, want them equal", before, again)
	}
	if after := etagOf(indexHTML + "<p>new build</p>"); after == before {
		t.Errorf("changed content: ETag stayed %q", before)
	}
}

func TestUIDeepLinksGetIndexETag(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{}, nil, nil)
	indexETag := get(t, s, "/").Header().Get("ETag")

	for _, target := range []string{"/history", "/a/b/c", "/assets"} {
		if etag := get(t, s, target).Header().Get("ETag"); etag != indexETag {
			t.Errorf("GET %s: ETag = %q, want index.html's %q", target, etag, indexETag)
		}
		if rec := getWithETag(t, s, target, indexETag); rec.Code != http.StatusNotModified {
			t.Errorf("GET %s with index.html's ETag: status = %d, want %d", target, rec.Code, http.StatusNotModified)
		}
	}
}
