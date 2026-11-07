// Package web holds the built Svelte frontend so it can be compiled into the
// pisens binary. Run `make web` (or `npm run build` in this directory) first;
// without it the binary only contains the web/dist/.gitkeep placeholder.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the contents of web/dist, rooted so that index.html is at the
// top level.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// fs.Sub only fails on an invalid path, and "dist" is a constant.
		panic(err)
	}
	return sub
}
