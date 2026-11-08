package server

import (
	"io/fs"
	"regexp"
)

// buildMetaPattern finds the build ID that the frontend build stamps into
// index.html (web/vite-plugin-build-id.ts). The ID changes whenever the
// frontend does.
var buildMetaPattern = regexp.MustCompile(`<meta\s+name="pisens-build"\s+content="([^"<>]+)"`)

// webBuild returns the build ID of the frontend in ui, or "" when it has none:
// the frontend was not built, or its index.html predates build IDs.
func webBuild(ui fs.FS) string {
	data, err := fs.ReadFile(ui, "index.html")
	if err != nil {
		return ""
	}
	m := buildMetaPattern.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return string(m[1])
}
