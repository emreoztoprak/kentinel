package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/emreoztoprak/kentinel/internal/k8s"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := s.k8s.GetOverview(r.Context())
	if err != nil {
		s.log.Error("overview failed", "error", err)
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	namespaces, err := s.k8s.ListNamespaces(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"namespaces": namespaces})
}

func (s *Server) handleKinds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"kinds": k8s.SupportedKinds()})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.k8s.ListEvents(r.Context(), r.URL.Query().Get("namespace"), r.URL.Query().Get("type"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"events": events})
}

func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	items, err := s.k8s.ListResources(r.Context(), kind, r.URL.Query().Get("namespace"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

func (s *Server) handleGetResource(w http.ResponseWriter, r *http.Request) {
	detail, err := s.k8s.GetResource(r.Context(),
		chi.URLParam(r, "kind"), chi.URLParam(r, "namespace"), chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

type updateResourceRequest struct {
	YAML string `json:"yaml"`
}

func (s *Server) handleUpdateResource(w http.ResponseWriter, r *http.Request) {
	var req updateResourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, "request body must be JSON: {\"yaml\": \"...\"}")
		return
	}
	if req.YAML == "" {
		writeBadRequest(w, "yaml field is required")
		return
	}

	kind := chi.URLParam(r, "kind")
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	detail, err := s.k8s.UpdateResource(r.Context(), kind, namespace, name, req.YAML)
	if err != nil {
		s.log.Warn("resource update failed", "kind", kind, "namespace", namespace, "name", name, "error", err)
		writeError(w, err)
		return
	}
	s.log.Info("resource updated", "kind", kind, "namespace", namespace, "name", name)
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handlePodContainers(w http.ResponseWriter, r *http.Request) {
	containers, err := s.k8s.PodContainers(r.Context(), chi.URLParam(r, "namespace"), chi.URLParam(r, "name"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"containers": containers})
}
