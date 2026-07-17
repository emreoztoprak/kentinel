package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/emreoztoprak/kentinel/internal/config"
)

// proposalIDRe matches the agent's hex proposal IDs. Validating before the ID
// reaches an outbound URL removes any path/query-manipulation ambiguity.
var proposalIDRe = regexp.MustCompile(`^[a-f0-9]{1,64}$`)

// requireAssisted rejects mutating requests unless the deployment runs in
// assisted mode. The server ServiceAccount's RBAC is the primary enforcement
// (in readonly mode it literally lacks write/exec verbs); this middleware
// turns that into a clear 403 instead of a raw Kubernetes "forbidden".
func (s *Server) requireAssisted(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.mode != config.ModeAssisted {
			writeJSON(w, http.StatusForbidden, errorResponse{
				Error:   "readonly_mode",
				Message: "Kentinel is running in read-only mode; changing cluster resources is disabled. Redeploy with mode=assisted to enable approval-gated changes.",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// agentProposal mirrors the fields of agent.Proposal the server needs to
// apply a change. Duplicated rather than imported — server and agent are
// independently deployable and share only a versioned HTTP/JSON contract.
type agentProposal struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Kind         string `json:"kind"`
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	ProposedYAML string `json:"proposedYaml"`
}

// handleApplyProposal applies an approved remediation proposal. Flow:
//  1. fetch the proposal from the agent by ID (so the applied content is
//     exactly what was reviewed — the client can't substitute a payload);
//  2. verify it is still pending;
//  3. apply via the same guarded UpdateResource path the manifest editor
//     uses (kind/name/namespace must match);
//  4. tell the agent to record the outcome (applied or failed).
//
// The agent never applies — only this server path does, and only in assisted
// mode (route is wrapped in requireAssisted).
func (s *Server) handleApplyProposal(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !proposalIDRe.MatchString(id) {
		writeBadRequest(w, "invalid proposal id")
		return
	}

	prop, err := s.fetchProposal(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "agent_unavailable", Message: err.Error()})
		return
	}
	if prop.Status != "pending" {
		writeJSON(w, http.StatusConflict, errorResponse{
			Error:   "not_pending",
			Message: fmt.Sprintf("proposal %s is %s, not pending", id, prop.Status),
		})
		return
	}

	// Apply with the same identity guard as the manifest editor.
	_, applyErr := s.k8s.UpdateResource(r.Context(), prop.Kind, prop.Namespace, prop.Name, prop.ProposedYAML)

	// Record the outcome on the agent regardless of success, so the audit
	// trail reflects reality. A failed resolve is logged but doesn't change
	// the HTTP result the user sees for the apply itself.
	resolveMsg := ""
	if applyErr != nil {
		resolveMsg = applyErr.Error()
	}
	if err := s.resolveProposal(r.Context(), id, resolveMsg); err != nil {
		s.log.Error("recording proposal outcome on agent failed", "id", id, "error", err)
	}

	if applyErr != nil {
		s.log.Warn("applying proposal failed", "id", id, "kind", prop.Kind, "namespace", prop.Namespace, "name", prop.Name, "error", applyErr)
		writeError(w, applyErr)
		return
	}
	s.log.Info("proposal applied", "id", id, "kind", prop.Kind, "namespace", prop.Namespace, "name", prop.Name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied", "id": id})
}

func (s *Server) fetchProposal(ctx context.Context, id string) (agentProposal, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.agentURL, "/")+"/proposals/"+id, nil)
	if err != nil {
		return agentProposal{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return agentProposal{}, fmt.Errorf("agent not reachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return agentProposal{}, fmt.Errorf("proposal %s not found", id)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return agentProposal{}, fmt.Errorf("agent returned HTTP %d fetching proposal: %s", resp.StatusCode, string(body))
	}
	var p agentProposal
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return agentProposal{}, fmt.Errorf("decoding proposal: %w", err)
	}
	return p, nil
}

func (s *Server) resolveProposal(ctx context.Context, id, applyErr string) error {
	body, _ := json.Marshal(map[string]string{"error": applyErr})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.agentURL, "/")+"/proposals/"+id+"/resolve", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("agent returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
