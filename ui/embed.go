// Package ui embeds the built admin UI (Vite output in dist/) and serves it.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the single-page admin UI. Unknown paths fall back to
// index.html so client-side routing works.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if _, err := fs.Stat(sub, r.URL.Path[1:]); err != nil {
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}
