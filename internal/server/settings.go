package server

import (
	"net/http"
	"os"
	"strings"
)

// serviceAccountNamespaceFile exists only inside a pod.
const serviceAccountNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// handleServerSettings returns the server's own (read-only) configuration —
// shown on the Settings page for visibility.
func (s *Server) handleServerSettings(w http.ResponseWriter, r *http.Request) {
	inCluster, namespace := clusterContext()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agentUrl":         s.agentURL,
		"staticDir":        s.staticDir,
		"inCluster":        inCluster,
		"namespace":        namespace,
		"supportedByProxy": true,
	})
}

// clusterContext reports whether we run inside a pod, and in which namespace.
func clusterContext() (bool, string) {
	data, err := os.ReadFile(serviceAccountNamespaceFile)
	if err != nil {
		return false, ""
	}
	return true, strings.TrimSpace(string(data))
}
