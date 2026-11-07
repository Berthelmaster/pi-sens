package auth

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// testHash hashes password at the lowest cost so the tests stay fast.
func testHash(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

func newTestBasic(t *testing.T, logger *slog.Logger) *Basic {
	t.Helper()
	b, err := NewBasic("admin", testHash(t, "s3cret pass"), logger)
	if err != nil {
		t.Fatalf("NewBasic() error = %v", err)
	}
	return b
}

func TestMiddleware(t *testing.T) {
	var logs bytes.Buffer
	b := newTestBasic(t, slog.New(slog.NewTextHandler(&logs, nil)))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok " + r.URL.Path))
	})
	h := b.Middleware(next, "/api/health")

	tests := []struct {
		name       string
		path       string
		user, pass string
		noAuth     bool
		wantCode   int
		wantLogged bool
	}{
		{name: "right credentials", path: "/", user: "admin", pass: "s3cret pass", wantCode: http.StatusOK},
		{name: "right credentials on the stream", path: "/api/stream", user: "admin", pass: "s3cret pass", wantCode: http.StatusOK},
		{name: "no credentials", path: "/", noAuth: true, wantCode: http.StatusUnauthorized},
		{name: "no credentials on the api", path: "/api/metrics", noAuth: true, wantCode: http.StatusUnauthorized},
		{name: "wrong password", path: "/", user: "admin", pass: "s3cret", wantCode: http.StatusUnauthorized, wantLogged: true},
		{name: "wrong user", path: "/api/metrics", user: "root", pass: "s3cret pass", wantCode: http.StatusUnauthorized, wantLogged: true},
		{name: "user prefix", path: "/", user: "adm", pass: "s3cret pass", wantCode: http.StatusUnauthorized, wantLogged: true},
		{name: "empty credentials", path: "/", wantCode: http.StatusUnauthorized, wantLogged: true},
		{name: "health without credentials", path: "/api/health", noAuth: true, wantCode: http.StatusOK},
		{name: "health with wrong credentials", path: "/api/health", user: "root", pass: "x", wantCode: http.StatusOK},
		// Only the exact path is open.
		{name: "below health", path: "/api/health/x", noAuth: true, wantCode: http.StatusUnauthorized},
		{name: "health with trailing slash", path: "/api/health/", noAuth: true, wantCode: http.StatusUnauthorized},
		{name: "health prefix", path: "/api/healthz", noAuth: true, wantCode: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs.Reset()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if !tt.noAuth {
				req.SetBasicAuth(tt.user, tt.pass)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantCode, rec.Body)
			}
			challenge := rec.Header().Get("WWW-Authenticate")
			if tt.wantCode == http.StatusUnauthorized {
				if !strings.HasPrefix(challenge, `Basic realm="pisens"`) {
					t.Errorf("WWW-Authenticate = %q, want a Basic challenge", challenge)
				}
				if got := rec.Body.String(); got != "{\"error\":\"unauthorized\"}\n" {
					t.Errorf("body = %q", got)
				}
				if got := rec.Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("Cache-Control = %q, want no-store", got)
				}
			} else {
				if challenge != "" {
					t.Errorf("WWW-Authenticate = %q on an allowed request", challenge)
				}
				if got := rec.Body.String(); got != "ok "+tt.path {
					t.Errorf("body = %q, want the next handler's", got)
				}
			}
			if logged := strings.Contains(logs.String(), "basic auth failed"); logged != tt.wantLogged {
				t.Errorf("logged failure = %v, want %v: %s", logged, tt.wantLogged, logs.String())
			}
			if tt.pass != "" && strings.Contains(logs.String(), tt.pass) {
				t.Errorf("log contains the password: %s", logs.String())
			}
		})
	}
}

func TestCheckRemembersVerifiedPassword(t *testing.T) {
	b := newTestBasic(t, nil)
	if b.verified.Load() != nil {
		t.Fatal("verified is set before any check")
	}
	if b.Check("admin", "wrong") {
		t.Fatal("Check() = true for a wrong password")
	}
	if b.verified.Load() != nil {
		t.Fatal("a wrong password was remembered")
	}
	for i := range 3 {
		if !b.Check("admin", "s3cret pass") {
			t.Fatalf("Check() #%d = false for the right password", i)
		}
	}
	if b.verified.Load() == nil {
		t.Fatal("the right password was not remembered")
	}
	// The remembered password does not open the door for another user name
	// or a different password.
	if b.Check("root", "s3cret pass") {
		t.Error("Check() = true for a wrong user with the remembered password")
	}
	if b.Check("admin", "s3cret pass ") {
		t.Error("Check() = true for a different password after one was remembered")
	}
}

func TestValidate(t *testing.T) {
	hash := testHash(t, "pw")
	tests := []struct {
		name, user, hash string
		wantErr          string
	}{
		{name: "valid", user: "admin", hash: hash},
		{name: "empty user", user: "", hash: hash, wantErr: "must not be empty"},
		{name: "colon in user", user: "ad:min", hash: hash, wantErr: "colon"},
		{name: "plain password as hash", user: "admin", hash: "hunter2", wantErr: "not a bcrypt hash"},
		{name: "empty hash", user: "admin", hash: "", wantErr: "not a bcrypt hash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.user, tt.hash)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want one containing %q", err, tt.wantErr)
			}
			if tt.hash != "" && strings.Contains(err.Error(), tt.hash) {
				t.Errorf("error repeats the hash: %v", err)
			}
		})
	}
	if _, err := NewBasic("admin", "hunter2", nil); err == nil {
		t.Error("NewBasic() accepted a plain password as the hash")
	}
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse")); err != nil {
		t.Errorf("hash does not match the password: %v", err)
	}
	if cost, _ := bcrypt.Cost([]byte(hash)); cost != bcrypt.DefaultCost {
		t.Errorf("cost = %d, want %d", cost, bcrypt.DefaultCost)
	}
	if _, err := HashPassword(""); err == nil {
		t.Error("HashPassword(\"\") error = nil")
	}
	if _, err := HashPassword(strings.Repeat("x", 73)); err == nil {
		t.Error("HashPassword() of 73 bytes error = nil; bcrypt would ignore the rest")
	}
}
