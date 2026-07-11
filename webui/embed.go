package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

func Static() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
