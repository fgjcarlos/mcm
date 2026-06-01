package frontend

import (
	"embed"
	"io/fs"
)

//go:embed dist/index.html
var entrypoint string

//go:embed all:dist
var assets embed.FS

// DistFS returns the frontend build output as a filesystem rooted at dist/.
func DistFS() (fs.FS, error) {
	_ = entrypoint // Force dist/index.html to exist at compile time.
	return fs.Sub(assets, "dist")
}
