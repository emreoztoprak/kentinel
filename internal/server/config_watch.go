package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/emreoztoprak/kentinel/internal/config"
)

// DefaultConfigWatchInterval is how often WatchAgentConfig polls for
// changes. A poll (not a k8s watch) is deliberate: it's a handful of lines
// instead of an informer with reconnect/resync handling, the two objects it
// reads are tiny, and a 30s worst-case delay between "helm upgrade" and the
// agent noticing is an easy trade for that simplicity.
const DefaultConfigWatchInterval = 30 * time.Second

// settingsSyncPayload mirrors agent.SettingsSync's wire shape. Duplicated
// rather than imported — server and agent are independently deployable
// processes that only share a versioned HTTP/JSON contract, the same
// reason the codebase already keeps separate copies of the settings wire
// structs on each side.
type settingsSyncPayload struct {
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	OllamaHost     string            `json:"ollamaHost"`
	APIKeys        map[string]string `json:"apiKeys"`
	ReviewInterval string            `json:"reviewInterval"`
	MonitorEnabled bool              `json:"monitorEnabled"`

	NotificationsEnabled bool   `json:"notificationsEnabled"`
	DiscordWebhook       string `json:"discordWebhookUrl"`
	SlackWebhook         string `json:"slackWebhookUrl"`
	TeamsWebhook         string `json:"teamsWebhookUrl"`
	NotifyMinSeverity    string `json:"notifyMinSeverity"`

	PrometheusURL string `json:"prometheusUrl"`
}

// WatchAgentConfig polls the agent's ConfigMap/Secret for changes made
// outside the Settings UI — a `helm upgrade --set`, a `kubectl edit` or
// `kubectl apply` — and pushes the result to the agent through its
// authoritative sync endpoint. A UI save and a Kubernetes-side change are
// symmetric: whichever happened most recently is what's running, no
// per-field tracking. Only runs in-cluster (Docker mode has no ConfigMap to
// watch) and blocks until ctx is cancelled, so call it in its own goroutine.
func (s *Server) WatchAgentConfig(ctx context.Context, interval time.Duration) {
	inCluster, namespace := clusterContext()
	if !inCluster {
		return
	}
	if interval <= 0 {
		interval = DefaultConfigWatchInterval
	}

	var lastConfigVersion, lastSecretVersion string
	seeded := false

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		cm, cmErr := s.k8s.Clientset.CoreV1().ConfigMaps(namespace).Get(ctx, agentConfigMapName, metav1.GetOptions{})
		secret, secretErr := s.k8s.Clientset.CoreV1().Secrets(namespace).Get(ctx, agentSecretName, metav1.GetOptions{})
		if cmErr != nil || secretErr != nil {
			continue // transient API hiccups: try again next tick, no need to log every 30s
		}

		changed := cm.ResourceVersion != lastConfigVersion || secret.ResourceVersion != lastSecretVersion
		lastConfigVersion, lastSecretVersion = cm.ResourceVersion, secret.ResourceVersion

		if !seeded {
			// The agent already read these same values from envFrom at its
			// own boot; pushing now would just be a redundant write, and
			// worse, could clobber a UI change made between this server's
			// startup and its first tick. Only react to genuine changes
			// from here on.
			seeded = true
			continue
		}
		if !changed {
			continue
		}

		if err := s.pushConfigToAgent(ctx, cm, secret); err != nil {
			s.log.Error("syncing agent settings from ConfigMap/Secret failed", "error", err)
		} else {
			s.log.Info("agent settings synced from ConfigMap/Secret", "namespace", namespace)
		}
	}
}

// pushConfigToAgent reads the ConfigMap/Secret's current values and PUTs
// them to the agent's authoritative sync endpoint.
func (s *Server) pushConfigToAgent(ctx context.Context, cm *corev1.ConfigMap, secret *corev1.Secret) error {
	data := cm.Data
	provider := data["LLM_PROVIDER"]

	apiKeys := map[string]string{}
	for p, envName := range config.APIKeyEnvNames {
		if key := string(secret.Data[envName]); key != "" && key != "REPLACE_ME" {
			apiKeys[p] = key
		}
	}

	payload := settingsSyncPayload{
		Provider:       provider,
		Model:          data["LLM_MODEL"],
		OllamaHost:     data["OLLAMA_HOST"],
		APIKeys:        apiKeys,
		ReviewInterval: data["AGENT_REVIEW_INTERVAL"],
		MonitorEnabled: data["AGENT_MONITOR_ENABLED"] == "true",

		NotificationsEnabled: data["NOTIFICATIONS_ENABLED"] == "true",
		DiscordWebhook:       string(secret.Data["DISCORD_WEBHOOK_URL"]),
		SlackWebhook:         string(secret.Data["SLACK_WEBHOOK_URL"]),
		TeamsWebhook:         string(secret.Data["TEAMS_WEBHOOK_URL"]),
		NotifyMinSeverity:    data["NOTIFY_MIN_SEVERITY"],

		PrometheusURL: data["PROMETHEUS_URL"],
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding sync payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		strings.TrimRight(s.agentURL, "/")+"/config/sync", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building sync request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("agent rejected settings sync (HTTP %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}
