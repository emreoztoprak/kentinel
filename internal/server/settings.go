package server

import (
	"net/http"
	"os"
	"strings"
)

// Names of the agent's ConfigMap/Secret. The raw manifests use the
// defaults; the Helm chart overrides them via env because its object names
// carry the release prefix. The server only ever reads these (to poll for
// changes — see config_watch.go); it never writes to them. Settings changed
// from the UI are persisted by the agent itself, to its own database.
var (
	agentConfigMapName = envOr("AGENT_CONFIGMAP_NAME", "agent-config")
	agentSecretName    = envOr("AGENT_SECRET_NAME", "agent-secrets")
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

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
