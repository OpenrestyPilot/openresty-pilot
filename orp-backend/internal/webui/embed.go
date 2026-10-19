package webui

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// static contains the source placeholder and the generated frontend under
// static/dist when the release build script runs.
//
//go:embed all:static
var static embed.FS

// Handler serves the embedded frontend and falls back to index.html for
// client-side routes. Unknown API paths remain 404 responses.
func Handler() http.Handler {
	root, err := fs.Sub(static, "static")
	if err != nil {
		return http.NotFoundHandler()
	}
	frontend := root
	if built, err := fs.Sub(root, "dist"); err == nil {
		if _, err := fs.Stat(built, "index.html"); err == nil {
			frontend = built
		}
	}
	files := http.FileServer(http.FS(frontend))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		if file, err := frontend.Open(name); err == nil {
			_ = file.Close()
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		index, err := frontend.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer index.Close()
		content, err := io.ReadAll(index)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		info, err := index.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "index.html", info.ModTime(), bytes.NewReader(content))
	})
}
