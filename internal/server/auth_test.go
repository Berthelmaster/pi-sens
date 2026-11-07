package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/Berthelmaster/pi-sens/internal/auth"
)

func newTestAuth(t *testing.T) *auth.Basic {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	b, err := auth.NewBasic("admin", string(hash), nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// authTestPaths covers every kind of route: the API, the SPA and its assets,
// and unknown paths.
var authTestPaths = []string{
	"/", "/index.html", "/history", "/assets/index-abc.js",
	"/api/metrics", "/api/alerts", "/api/series", "/api/history?series=cpu.usage_pct", "/api/nope",
}

func TestBasicAuthOffByDefault(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{snap: snapAt(1), ok: true}, nil, nil)
	for _, path := range append(authTestPaths, "/api/health") {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "" {
			t.Errorf("GET %s = %d with WWW-Authenticate %q; want no auth without the option",
				path, rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
	}

	// A nil auth is the same as leaving the option out.
	s = New(":0", "dev", testUI, discardLogger(), &fakeSource{snap: snapAt(1), ok: true}, nil, nil, WithBasicAuth(nil))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/metrics with WithBasicAuth(nil) = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestBasicAuthProtectsEveryRouteButHealth(t *testing.T) {
	s := New(":0", "dev", testUI, discardLogger(), &fakeSource{snap: snapAt(1), ok: true}, nil, nil,
		WithBasicAuth(newTestAuth(t)))

	for _, path := range authTestPaths {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without credentials = %d, want %d", path, rec.Code, http.StatusUnauthorized)
		}

		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.SetBasicAuth("admin", "pw")
		rec = httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("GET %s with credentials = %d", path, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/health without credentials = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestStreamWithBasicAuth checks the SSE stream through the middleware over
// a real connection, the way a browser's EventSource sends the cached
// credentials.
func TestStreamWithBasicAuth(t *testing.T) {
	src := &fakeSource{snap: snapAt(1), ok: true}
	ts := httptest.NewServer(New(":0", "dev", testUI, discardLogger(), src, nil, nil,
		WithBasicAuth(newTestAuth(t))).Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stream without credentials = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	u := ts.URL[:len("http://")] + "admin:pw@" + ts.URL[len("http://"):]
	stream := openStream(t, context.Background(), u)
	if got := newSSEReader(t, stream.Body).nextSnapshot(); !got.Timestamp.Equal(snapAt(1).Timestamp) {
		t.Fatalf("first snapshot = %+v, want the latest one", got)
	}
}
