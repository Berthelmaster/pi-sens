package server

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
)

const (
	// cacheImmutable is for Vite's content-hashed files under assets/. Their
	// names change whenever their contents do, so browsers can keep them.
	cacheImmutable = "public, max-age=31536000, immutable"
	// cacheRevalidate is for everything else, index.html in particular, so a
	// new binary's frontend is picked up on the next load.
	cacheRevalidate = "no-cache"
)

// spaHandler serves the frontend from ui. Paths that match a file serve that
// file. Other paths without a file extension serve index.html so client-side
// routes such as /history work as deep links. Unknown /api paths get a JSON
// 404 instead of the page.
//
// Files in an embed.FS have no ModTime, so every file gets a strong ETag from
// a hash of its contents instead. That lets no-cache files such as index.html
// revalidate with a 304 rather than download again on every load.
type spaHandler struct {
	ui     fs.FS
	logger *slog.Logger
	// etags caches each file's ETag by name. ui does not change while the
	// server runs, so a file is hashed at most once.
	etags sync.Map // string -> string
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlPath := path.Clean("/" + r.URL.Path)
	if urlPath == "/api" || strings.HasPrefix(urlPath, "/api/") {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}

	name := strings.TrimPrefix(urlPath, "/")
	if name != "" && name != "index.html" {
		if h.serveFile(w, r, name) {
			return
		}
		// A missing file that looks like one (script, image, ...) is a 404.
		// Answering with HTML would only hide the broken reference.
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
	}

	if !h.serveFile(w, r, "index.html") {
		http.Error(w, "frontend not built: run `make web` and rebuild pisens", http.StatusServiceUnavailable)
	}
}

// serveFile writes the regular file name from ui and reports whether it
// handled the request. It returns false, having written nothing, when there is
// no such file so the caller can fall back. Dotfiles count as missing.
func (h *spaHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	if strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
		return false
	}
	f, err := h.ui.Open(name)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
		return false
	}
	if err != nil {
		h.fail(w, name, err)
		return true
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		h.fail(w, name, err)
		return true
	}
	if info.IsDir() {
		return false
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		h.fail(w, name, errors.New("file does not support seeking"))
		return true
	}
	etag, err := h.etag(name, rs)
	if err != nil {
		h.fail(w, name, err)
		return true
	}

	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", cacheImmutable)
	} else {
		w.Header().Set("Cache-Control", cacheRevalidate)
	}
	w.Header().Set("ETag", etag)
	http.ServeContent(w, r, info.Name(), info.ModTime(), rs)
	return true
}

// etag returns the strong ETag for the file name with contents rs, hashing rs
// the first time name is seen. It leaves rs at the start of the file.
func (h *spaHandler) etag(name string, rs io.ReadSeeker) (string, error) {
	if v, ok := h.etags.Load(name); ok {
		return v.(string), nil
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, rs); err != nil {
		return "", err
	}
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	etag := `"` + base64.RawURLEncoding.EncodeToString(sum.Sum(nil)[:16]) + `"`
	h.etags.Store(name, etag)
	return etag, nil
}

func (h *spaHandler) fail(w http.ResponseWriter, name string, err error) {
	h.logger.Error("serve ui file", "name", name, "err", err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}
