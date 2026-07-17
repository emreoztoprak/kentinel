package server

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// agentProxy forwards /api/v1/agent/* to the agent service, stripping the
// prefix (so /api/v1/agent/status → {AGENT_URL}/status). SSE responses are
// flushed immediately because httputil.ReverseProxy sets FlushInterval.
func (s *Server) agentProxy() http.Handler {
	target, err := url.Parse(s.agentURL)
	if err != nil {
		s.log.Error("invalid AGENT_URL, agent features disabled", "url", s.agentURL, "error", err)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusBadGateway, errorResponse{
				Error:   "agent_unavailable",
				Message: "AGENT_URL is misconfigured",
			})
		})
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/api/v1/agent")
			if pr.Out.URL.Path == "" {
				pr.Out.URL.Path = "/"
			}
		},
		// Negative = flush immediately; required for SSE passthrough.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.log.Warn("agent proxy error", "path", r.URL.Path, "error", err)
			writeJSON(w, http.StatusBadGateway, errorResponse{
				Error:   "agent_unavailable",
				Message: "the AI agent service is not reachable; check that it is running and AGENT_URL is correct",
			})
		},
	}

	// Block server-internal agent endpoints from the public proxy. /resolve
	// is called by the server directly (bypassing this proxy) after it
	// applies a proposal; exposing it here would let a UI caller spoof the
	// audit trail by marking proposals applied without applying them.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/resolve") {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "not_found", Message: "not found"})
			return
		}
		proxy.ServeHTTP(w, r)
	})
}
