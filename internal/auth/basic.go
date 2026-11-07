// Package auth implements the optional HTTP basic auth that protects the
// pisens dashboard and API.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"golang.org/x/crypto/bcrypt"
)

// Realm is the realm in the WWW-Authenticate challenge. Browsers show it in
// the login prompt.
const Realm = "pisens"

// Basic checks HTTP basic auth credentials against one user name and a
// bcrypt password hash.
type Basic struct {
	userSum [sha256.Size]byte
	hash    []byte
	logger  *slog.Logger

	// verified is the SHA-256 of the last password that matched hash. bcrypt
	// is slow on purpose and the dashboard sends the same credentials with
	// every request, so a password seen before is checked against this
	// instead. Only one password can match, so one slot is enough.
	verified atomic.Pointer[[sha256.Size]byte]
}

// NewBasic returns a Basic for user with the bcrypt hash of the password, as
// printed by "pisens hash-password". logger gets a warning for every request
// with wrong credentials; nil discards them.
func NewBasic(user, passwordHash string, logger *slog.Logger) (*Basic, error) {
	if err := Validate(user, passwordHash); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Basic{
		userSum: sha256.Sum256([]byte(user)),
		hash:    []byte(passwordHash),
		logger:  logger,
	}, nil
}

// Validate reports whether user and passwordHash can be used for basic auth:
// the user must be non-empty without a colon, which basic auth uses as the
// separator, and the hash must be a bcrypt hash.
func Validate(user, passwordHash string) error {
	var errs []error
	if user == "" {
		errs = append(errs, errors.New("auth user must not be empty"))
	}
	if strings.Contains(user, ":") {
		errs = append(errs, errors.New("auth user must not contain a colon"))
	}
	// The error leaves the hash out; it is not the password, but there is no
	// reason to put it in logs either.
	if _, err := bcrypt.Cost([]byte(passwordHash)); err != nil {
		errs = append(errs, errors.New("auth password hash is not a bcrypt hash; create one with: pisens hash-password"))
	}
	return errors.Join(errs...)
}

// HashPassword returns the bcrypt hash of password at the default cost.
// bcrypt only uses the first 72 bytes, so longer passwords are an error.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// Check reports whether user and password are the configured credentials.
// It takes about as long whether the user name is right or not.
func (b *Basic) Check(user, password string) bool {
	// Comparing fixed-size hashes keeps the comparison constant-time in the
	// length of the user name too.
	userSum := sha256.Sum256([]byte(user))
	userOK := subtle.ConstantTimeCompare(userSum[:], b.userSum[:]) == 1
	// The password is always checked, so a wrong user name is not faster.
	passwordOK := b.checkPassword(password)
	return userOK && passwordOK
}

func (b *Basic) checkPassword(password string) bool {
	sum := sha256.Sum256([]byte(password))
	if v := b.verified.Load(); v != nil && subtle.ConstantTimeCompare(v[:], sum[:]) == 1 {
		return true
	}
	if bcrypt.CompareHashAndPassword(b.hash, []byte(password)) != nil {
		return false
	}
	b.verified.Store(&sum)
	return true
}

// Middleware requires basic auth for every request to next except those for
// exactly one of openPaths.
//
// Browsers answer the challenge with a login prompt and then send the
// credentials with every same-origin request, EventSource included, so a
// page and its live stream work unchanged once the user has logged in.
func (b *Basic) Middleware(next http.Handler, openPaths ...string) http.Handler {
	open := make(map[string]bool, len(openPaths))
	for _, p := range openPaths {
		open[p] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if open[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		user, password, ok := r.BasicAuth()
		if ok && b.Check(user, password) {
			next.ServeHTTP(w, r)
			return
		}
		if ok {
			// A request without credentials is the normal first step of a
			// browser login, so only wrong credentials are logged. The user
			// name is left out in case a password was typed into it.
			b.logger.Warn("basic auth failed", "remote", r.RemoteAddr, "path", r.URL.Path)
		}
		h := w.Header()
		h.Set("WWW-Authenticate", `Basic realm="`+Realm+`", charset="UTF-8"`)
		h.Set("Content-Type", "application/json")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
	})
}
