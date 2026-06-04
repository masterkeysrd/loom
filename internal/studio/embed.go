package studio

import (
	"embed"
	"io/fs"
)

// UI contains the static assets for the Loom Studio frontend.
//
//go:embed ui/dist/*
var uiFS embed.FS

// StaticAssets returns the file system for the UI dist directory.
func StaticAssets() fs.FS {
	sub, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		panic(err)
	}
	return sub
}
