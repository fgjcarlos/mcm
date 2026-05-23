package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// DistFS returns the frontend build output as a filesystem rooted at dist/.
func DistFS() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}
