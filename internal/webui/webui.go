// Package webui embeds the built frontend so the Go binary can serve it
// directly, with no external file dependencies at runtime (§2.1: "the
// built frontend ... is embedded into the binary via Go's embed.FS and
// served directly. go build produces one artifact that contains
// everything.").
//
// dist is populated by `npm run build` in web/, which writes straight
// into this package's dist subdirectory (see web/vite.config.ts's
// build.outDir) rather than web/dist — embed.FS can only embed files
// within or below the directory containing the //go:embed directive, and
// web/ isn't a subdirectory of cmd/argos or internal/webui. Only
// dist/index.html is committed, as a placeholder so `go build`/`go test`
// always succeed even before the real frontend has been built; a real
// `npm run build` overwrites it along with the rest of dist/.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded frontend build output, rooted at its own
// files (e.g. "index.html", "assets/...") rather than under a "dist/"
// prefix.
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
