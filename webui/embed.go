package webui

import (
	"embed"
	"io/fs"
	"mime"
)

//go:embed all:dist
var embedded embed.FS

func init() {
	// Go's built-in extension table carries no font types, and a trimmed-down
	// host may ship no /etc/mime.types either, which would hand the webfonts to
	// the browser as application/octet-stream.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

func Static() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
