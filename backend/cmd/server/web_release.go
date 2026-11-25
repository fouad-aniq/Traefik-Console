//go:build embedded_ui

package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist/index.html dist/assets/*
var webFiles embed.FS

func init() {
	root, err := fs.Sub(webFiles, "dist")
	if err != nil {
		panic(err)
	}
	webHandler = http.FileServerFS(root)
}
