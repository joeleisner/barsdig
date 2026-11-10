package server

import (
	"embed"
	"io/fs"
)

//go:embed web
var webFiles embed.FS

func webFS() fs.FS {
	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	return sub
}
