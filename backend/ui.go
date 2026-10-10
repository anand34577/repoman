package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The React app is built into ui/dist (see frontend/vite.config.ts) and shipped inside the binary.
//
//go:embed all:ui
var uiFiles embed.FS

func uiHandler() http.Handler {
	dist, _ := fs.Sub(uiFiles, "ui/dist")
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if st, err := fs.Stat(dist, p); p != "" && err == nil && !st.IsDir() {
			if strings.HasPrefix(p, "assets/") { // hashed filenames
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		index, err := fs.ReadFile(dist, "index.html") // SPA fallback
		if err != nil {
			http.Error(w, "UI not built: run `npm install && npm run build` in frontend/", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}
