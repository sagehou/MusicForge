package web

import (
	"embed"
	"io/fs"
)

// Assets are built only in CI, before the Go build.
//
//go:embed all:dist
var assets embed.FS

func Assets() fs.FS {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
