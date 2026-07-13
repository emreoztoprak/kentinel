package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// mountStatic serves the built SPA from staticDir with an index.html fallback
// for client-side routes. API and health routes take precedence because they
// are registered first.
func (s *Server) mountStatic(r chi.Router) {
	fileServer := http.FileServer(http.Dir(s.staticDir))
	index := filepath.Join(s.staticDir, "index.html")

	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/")
		if path == "" {
			http.ServeFile(w, req, index)
			return
		}
		full := filepath.Join(s.staticDir, filepath.Clean("/"+path))
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, req)
			return
		}
		// Unknown path → SPA route, let the client router handle it.
		http.ServeFile(w, req, index)
	})
}
