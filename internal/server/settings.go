package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/emreoztoprak/kentinel/internal/config"
)

// Names of the settings write-back targets. The raw manifests use the
// defaults; the Helm chart overrides them via env because its object names
// carry the release prefix.
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
		"settingsPersist":  inCluster, // UI hint: whether saves survive restarts
		"supportedByProxy": true,
	})
}

// agentConfigUpdate mirrors the agent's SettingsUpdate; the server only needs
// the fields it persists.
type agentConfigUpdate struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	OllamaHost     string `json:"ollamaHost"`
	APIKey         string `json:"apiKey,omitempty"` // for the selected provider
	ReviewInterval string `json:"reviewInterval"`
	MonitorEnabled bool   `json:"monitorEnabled"`

	NotificationsEnabled bool   `json:"notificationsEnabled"`
	DiscordWebhook       string `json:"discordWebhookUrl,omitempty"`
	SlackWebhook         string `json:"slackWebhookUrl,omitempty"`
	TeamsWebhook         string `json:"teamsWebhookUrl,omitempty"`
	NotifyMinSeverity    string `json:"notifyMinSeverity"`
	PrometheusURL        string `json:"prometheusUrl"`
}

// handleAgentConfigUpdate applies agent settings in two steps:
//  1. forward the update to the agent, which validates and live-applies it;
//  2. on success, persist to the agent-config ConfigMap (and the Secret when
//     a new API key was provided) so the settings survive pod restarts.
//
// Step 2 only runs in-cluster; in Docker mode changes are runtime-only.
func (s *Server) handleAgentConfigUpdate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeBadRequest(w, "reading request body: "+err.Error())
		return
	}
	var update agentConfigUpdate
	if err := json.Unmarshal(body, &update); err != nil {
		writeBadRequest(w, "invalid settings JSON: "+err.Error())
		return
	}

	// 1. Live-apply on the agent (it owns validation).
	agentReq, err := http.NewRequestWithContext(r.Context(), http.MethodPut,
		strings.TrimRight(s.agentURL, "/")+"/config", bytes.NewReader(body))
	if err != nil {
		writeError(w, err)
		return
	}
	agentReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(agentReq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{
			Error:   "agent_unavailable",
			Message: "the AI agent service is not reachable; settings were not changed",
		})
		return
	}
	defer resp.Body.Close()
	agentBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		// Pass the agent's validation error straight through.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(agentBody)
		return
	}

	// 2. Persist (best effort — the live change already succeeded).
	persisted := false
	persistError := ""
	if inCluster, namespace := clusterContext(); inCluster {
		if err := s.persistAgentConfig(r.Context(), namespace, update); err != nil {
			persistError = err.Error()
			s.log.Error("persisting agent settings failed", "error", err)
		} else {
			persisted = true
			s.log.Info("agent settings persisted", "configMap", agentConfigMapName, "namespace", namespace)
		}
	}

	var agentView map[string]interface{}
	_ = json.Unmarshal(agentBody, &agentView)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"config":       agentView,
		"persisted":    persisted,
		"persistError": persistError,
	})
}

// persistAgentConfig writes the settings into the ConfigMap the agent boots
// from, and the API key into the Secret (only when a new key was provided).
func (s *Server) persistAgentConfig(ctx context.Context, namespace string, update agentConfigUpdate) error {
	configMaps := s.k8s.Clientset.CoreV1().ConfigMaps(namespace)
	cm, err := configMaps.Get(ctx, agentConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("ConfigMap %s not found in namespace %s — apply the deploy/k8s manifests first", agentConfigMapName, namespace)
	}
	if err != nil {
		return fmt.Errorf("reading ConfigMap %s: %w", agentConfigMapName, err)
	}

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data["LLM_PROVIDER"] = update.Provider
	cm.Data["LLM_MODEL"] = update.Model
	cm.Data["OLLAMA_HOST"] = update.OllamaHost
	cm.Data["AGENT_REVIEW_INTERVAL"] = update.ReviewInterval
	cm.Data["AGENT_MONITOR_ENABLED"] = strconv.FormatBool(update.MonitorEnabled)
	cm.Data["NOTIFICATIONS_ENABLED"] = strconv.FormatBool(update.NotificationsEnabled)
	if update.NotifyMinSeverity != "" {
		cm.Data["NOTIFY_MIN_SEVERITY"] = update.NotifyMinSeverity
	}
	cm.Data["PROMETHEUS_URL"] = update.PrometheusURL // empty is meaningful: disables metrics

	if _, err := configMaps.Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating ConfigMap %s: %w", agentConfigMapName, err)
	}

	// Secret write-back: API key (keyed by the selected provider) and any
	// newly provided webhook URLs.
	secretUpdates := map[string]string{}
	if update.APIKey != "" {
		if envName, ok := config.APIKeyEnvNames[update.Provider]; ok {
			secretUpdates[envName] = update.APIKey
		}
	}
	if update.DiscordWebhook != "" {
		secretUpdates["DISCORD_WEBHOOK_URL"] = update.DiscordWebhook
	}
	if update.SlackWebhook != "" {
		secretUpdates["SLACK_WEBHOOK_URL"] = update.SlackWebhook
	}
	if update.TeamsWebhook != "" {
		secretUpdates["TEAMS_WEBHOOK_URL"] = update.TeamsWebhook
	}

	if len(secretUpdates) > 0 {
		secrets := s.k8s.Clientset.CoreV1().Secrets(namespace)
		secret, err := secrets.Get(ctx, agentSecretName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading Secret %s: %w", agentSecretName, err)
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		for k, v := range secretUpdates {
			secret.Data[k] = []byte(v)
		}
		if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("updating Secret %s: %w", agentSecretName, err)
		}
	}
	return nil
}

// clusterContext reports whether we run inside a pod, and in which namespace.
func clusterContext() (bool, string) {
	data, err := os.ReadFile(serviceAccountNamespaceFile)
	if err != nil {
		return false, ""
	}
	return true, strings.TrimSpace(string(data))
}
