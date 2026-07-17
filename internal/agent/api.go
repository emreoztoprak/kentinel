package agent

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	ollamallm "github.com/emreoztoprak/kentinel/internal/llm/ollama"
)

// API exposes the agent over HTTP. The UI backend proxies /api/v1/agent/* here.
type API struct {
	store    *Store
	query    *QueryEngine
	runtime  *Runtime
	notifier *Dispatcher
	log      *slog.Logger
}

// NewAPI wires the agent's HTTP surface.
func NewAPI(store *Store, query *QueryEngine, runtime *Runtime, notifier *Dispatcher, log *slog.Logger) *API {
	return &API{store: store, query: query, runtime: runtime, notifier: notifier, log: log}
}

// Router builds the agent's HTTP routes.
func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Get("/healthz", a.handleHealth)
	r.Get("/status", a.handleStatus)
	r.Get("/insights", a.handleInsights)
	r.Get("/insights/timeline", a.handleTimeline)
	r.Post("/query", a.handleQuery)
	r.Get("/config", a.handleGetConfig)
	r.Put("/config", a.handleUpdateConfig)
	r.Get("/models", a.handleModels)
	r.Post("/notifications/test", a.handleTestNotification)
	r.Get("/metrics/health", a.handleMetricsHealth)

	// Remediation proposals. The agent only reads/rejects/records them — it
	// never applies (no write RBAC). The server calls /resolve after it
	// applies an approved proposal.
	r.Get("/proposals", a.handleListProposals)
	r.Get("/proposals/{id}", a.handleGetProposal)
	r.Post("/proposals/{id}/reject", a.handleRejectProposal)
	r.Post("/proposals/{id}/resolve", a.handleResolveProposal)
	return r
}

// handleMetricsHealth checks Prometheus connectivity (Settings test button).
func (a *API) handleMetricsHealth(w http.ResponseWriter, r *http.Request) {
	prom := a.runtime.Prometheus()
	if prom == nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "metrics_disabled",
			"message": "no Prometheus URL is configured",
		})
		return
	}
	if err := prom.Healthy(r.Context()); err != nil {
		a.writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   "metrics_unreachable",
			"message": err.Error(),
		})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleTestNotification sends a test message to the configured webhook so
// users can verify the channel without breaking their cluster.
func (a *API) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	if err := a.notifier.SendTest(r.Context()); err != nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "notification_failed",
			"message": err.Error(),
		})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// handleModels lists selectable models for a provider: the installed models
// from the Ollama server, or the curated list for cloud providers. A fetch
// failure returns 200 with an error string so the UI can fall back to
// free-text input.
func (a *API) handleModels(w http.ResponseWriter, r *http.Request) {
	view := a.runtime.View()
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		provider = view.Provider
	}

	if provider == "ollama" {
		host := r.URL.Query().Get("host")
		if host == "" {
			host = view.OllamaHost
		}
		models, err := ollamallm.ListModels(r.Context(), host)
		response := map[string]interface{}{
			"provider": provider,
			"models":   models,
			// Report a model that's actually installed as the default, so the
			// UI's "provider default" never points at a model the server
			// doesn't have (the hardcoded constant may not be pulled).
			"default": pickOllamaModel(DefaultModel(provider), models),
		}
		if err != nil {
			response["models"] = []string{}
			response["error"] = err.Error()
		}
		a.writeJSON(w, http.StatusOK, response)
		return
	}

	models := KnownModels(provider)
	if models == nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad_request",
			"message": "unknown provider " + provider,
		})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"provider": provider,
		"models":   models,
		"default":  DefaultModel(provider),
	})
}

// handleGetConfig returns the runtime settings (API key masked to a bool).
func (a *API) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, a.runtime.View())
}

// handleUpdateConfig validates and live-applies a settings update, then
// persists it to the agent's own database (encrypted) so it survives pod
// restarts. This is the only way to change settings after the very first
// boot — see Runtime.Apply and docs/security.md.
func (a *API) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var update SettingsUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad_request",
			"message": "invalid settings JSON: " + err.Error(),
		})
		return
	}

	// "Provider default" for Ollama (empty model) must resolve to a model the
	// server actually has — otherwise switching back to Ollama picks the
	// hardcoded default, which may not be pulled, and every review 404s.
	if update.Provider == "ollama" && update.Model == "" {
		if models, err := ollamallm.ListModels(r.Context(), update.OllamaHost); err == nil {
			update.Model = pickOllamaModel(DefaultModel("ollama"), models)
		}
	}

	view, err := a.runtime.Apply(update)
	if err != nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad_request",
			"message": err.Error(),
		})
		return
	}
	a.writeJSON(w, http.StatusOK, view)
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleStatus returns the latest insight plus provider info — the payload
// behind the dashboard AI panel.
func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	view := a.runtime.View()
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"provider":          view.Provider,
		"model":             view.Model,
		"latest":            a.store.Latest(), // null until the first review completes
		"historyPersistent": a.store.Persistent(),
	})
}

// handleInsights lists past reviews. Query params: limit (default 50, max
// 500), status (healthy|warning|critical|error), since/until (RFC3339).
func (a *API) handleInsights(w http.ResponseWriter, r *http.Request) {
	q := HistoryQuery{}
	params := r.URL.Query()
	if v := params.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Limit = n
		}
	}
	q.Status = Status(params.Get("status"))
	if v := params.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.Since = t
		}
	}
	if v := params.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.Until = t
		}
	}

	insights, err := a.store.History(q)
	if err != nil {
		a.log.Error("querying insight history failed", "error", err)
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal_error", "message": err.Error(),
		})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"insights":   insights,
		"persistent": a.store.Persistent(),
	})
}

// handleTimeline returns compact (time, status) points for the trend strip.
// Query param: hours (default 24, max 168).
func (a *API) handleTimeline(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			hours = n
		}
	}
	if hours > 168 {
		hours = 168
	}

	points, err := a.store.Timeline(time.Duration(hours) * time.Hour)
	if err != nil {
		a.log.Error("querying timeline failed", "error", err)
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal_error", "message": err.Error(),
		})
		return
	}
	if points == nil {
		points = []TimelinePoint{}
	}
	a.writeJSON(w, http.StatusOK, map[string]interface{}{
		"points":     points,
		"hours":      hours,
		"persistent": a.store.Persistent(),
	})
}

type queryRequest struct {
	Prompt string `json:"prompt"`
}

// handleQuery runs one agentic query, streaming progress as SSE events.
func (a *API) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req queryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prompt == "" {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad_request",
			"message": "request body must be JSON: {\"prompt\": \"...\"}",
		})
		return
	}
	if len(req.Prompt) > 8000 {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad_request",
			"message": "prompt is too long (max 8000 characters)",
		})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "internal_error",
			"message": "streaming not supported",
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	a.query.Run(r.Context(), req.Prompt, func(ev QueryEvent) {
		data, err := json.Marshal(ev)
		if err != nil {
			return
		}
		if _, err := w.Write([]byte("data: " + string(data) + "\n\n")); err != nil {
			return
		}
		flusher.Flush()
	})
}

// handleListProposals returns proposals, newest first. ?pending=true limits
// to the actionable set.
func (a *API) handleListProposals(w http.ResponseWriter, r *http.Request) {
	proposals, err := a.store.ListProposals(r.URL.Query().Get("pending") == "true")
	if err != nil {
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list_failed", "message": err.Error()})
		return
	}
	if proposals == nil {
		proposals = []Proposal{}
	}
	a.writeJSON(w, http.StatusOK, map[string]interface{}{"proposals": proposals})
}

func (a *API) handleGetProposal(w http.ResponseWriter, r *http.Request) {
	p, ok, err := a.store.GetProposal(chi.URLParam(r, "id"))
	if err != nil {
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get_failed", "message": err.Error()})
		return
	}
	if !ok {
		a.writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "proposal not found"})
		return
	}
	a.writeJSON(w, http.StatusOK, p)
}

func (a *API) handleRejectProposal(w http.ResponseWriter, r *http.Request) {
	if err := a.store.RejectProposal(chi.URLParam(r, "id")); err != nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reject_failed", "message": err.Error()})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// handleResolveProposal records the outcome of an approved+applied proposal.
// Called by the SERVER after it applies the change — not exposed to the UI.
func (a *API) handleResolveProposal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := a.store.ResolveProposal(chi.URLParam(r, "id"), body.Error); err != nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resolve_failed", "message": err.Error()})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

func (a *API) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		a.log.Warn("writing response failed", "error", err)
	}
}
