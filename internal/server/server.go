// Package server implements the UI backend: REST + streaming endpoints over
// the k8s package, a reverse proxy to the AI agent service, and static file
// serving for the built SPA.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/emreoztoprak/kentinel/internal/k8s"
)

// Server wires handlers to their dependencies.
type Server struct {
	k8s      *k8s.Client
	agentURL string
	log      *slog.Logger
	// staticDir holds the built SPA; empty disables static serving (dev mode,
	// where Vite serves the frontend and proxies /api here).
	staticDir string
	// version is the running release (e.g. "0.3.0", or "dev" outside a
	// released build) — surfaced via GET /api/v1/settings for the
	// dashboard's update-check card.
	version string
}

// New creates the server. staticDir may be empty.
func New(client *k8s.Client, agentURL, staticDir, version string, log *slog.Logger) *Server {
	return &Server{k8s: client, agentURL: agentURL, log: log, staticDir: staticDir, version: version}
}

// Router builds the chi router with all routes and middleware.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.requestLogger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", s.handleHealth)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/overview", s.handleOverview)
		r.Get("/namespaces", s.handleNamespaces)
		r.Get("/kinds", s.handleKinds)
		r.Get("/events", s.handleEvents)

		r.Route("/resources/{kind}", func(r chi.Router) {
			r.Get("/", s.handleListResources)
			r.Get("/{namespace}/{name}", s.handleGetResource)
			r.Put("/{namespace}/{name}", s.handleUpdateResource)
		})

		r.Get("/pods/{namespace}/{name}/containers", s.handlePodContainers)
		r.Get("/pods/{namespace}/{name}/logs", s.handlePodLogs)
		r.Get("/pods/{namespace}/{name}/exec", s.handlePodExec) // WebSocket

		r.Get("/settings", s.handleServerSettings)

		// Everything under /agent, including settings updates, is proxied
		// straight to the agent service — it owns validation and persistence.
		r.Handle("/agent/*", s.agentProxy())
	})

	if s.staticDir != "" {
		s.mountStatic(r)
	}

	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// requestLogger logs one line per request with method, path, status, duration.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		// Skip noisy static asset logs; API and errors are what matter.
		if ww.Status() >= 400 || len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
			s.log.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"duration", time.Since(start).Round(time.Millisecond).String(),
				"requestId", middleware.GetReqID(r.Context()),
			)
		}
	})
}
