package server

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
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
type spaHandler struct {
	ui     fs.FS
	logger *slog.Logger
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
func (h spaHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
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

	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", cacheImmutable)
	} else {
		w.Header().Set("Cache-Control", cacheRevalidate)
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), rs)
	return true
}

func (h spaHandler) fail(w http.ResponseWriter, name string, err error) {
	h.logger.Error("serve ui file", "name", name, "err", err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}
