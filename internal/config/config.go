// Package config loads environment-based configuration for the server and
// agent binaries. Every option has a sensible default so both binaries run
// with zero configuration against a local kubeconfig.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Server holds configuration for the UI backend.
type Server struct {
	Port       int
	AgentURL   string
	Kubeconfig string // empty = auto (KUBECONFIG env, ~/.kube/config, in-cluster)
	LogLevel   string
	LogFormat  string
}

// Agent holds configuration for the AI agent service.
type Agent struct {
	Port       int
	Kubeconfig string
	LogLevel   string
	LogFormat  string
	// Provider: "ollama" (default, keyless) or a cloud provider —
	// "anthropic", "openai", "deepseek", "gemini".
	Provider string
	Model    string // empty = provider default
	// APIKeys maps cloud provider name -> API key.
	APIKeys        map[string]string
	OllamaHost     string
	ReviewInterval time.Duration
	MonitorEnabled bool

	// Notifications (webhooks; alerts on status transitions).
	NotificationsEnabled bool
	DiscordWebhook       string
	SlackWebhook         string
	TeamsWebhook         string
	NotifyMinSeverity    string // "warning" or "critical"

	// Insight persistence. Empty path = in-memory only (history is lost on
	// restart).
	InsightDBPath        string
	InsightRetentionDays int

	// Prometheus base URL for the agent's metrics tools. Empty = metrics
	// tools disabled.
	PrometheusURL string
}

// Defaults for the LLM providers. Overridable via LLM_MODEL.
const (
	DefaultAnthropicModel = "claude-opus-4-8"
	// Matches the model the bundled Ollama pulls (chart ollama.model /
	// deploy/k8s LLM_MODEL) so "provider default" resolves to an installed
	// model even when the server can't be queried. Small enough for modest
	// clusters (~1.5GB RAM) and tool-calling capable.
	DefaultOllamaModel = "qwen3:0.6b"
)

// APIKeyEnvNames maps each cloud provider to its API key env var (also the
// key name in the agent-secrets Secret).
var APIKeyEnvNames = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY",
	"openai":    "OPENAI_API_KEY",
	"deepseek":  "DEEPSEEK_API_KEY",
	"gemini":    "GEMINI_API_KEY",
}

// LoadServer reads server configuration from the environment.
func LoadServer() (*Server, error) {
	port, err := envInt("PORT", 8080)
	if err != nil {
		return nil, err
	}
	return &Server{
		Port:       port,
		AgentURL:   envStr("AGENT_URL", "http://localhost:8090"),
		Kubeconfig: envStr("KUBECONFIG_PATH", ""),
		LogLevel:   envStr("LOG_LEVEL", "info"),
		LogFormat:  envStr("LOG_FORMAT", "text"),
	}, nil
}

// LoadAgent reads agent configuration from the environment.
func LoadAgent() (*Agent, error) {
	port, err := envInt("PORT", 8090)
	if err != nil {
		return nil, err
	}
	interval, err := envDuration("AGENT_REVIEW_INTERVAL", 5*time.Minute)
	if err != nil {
		return nil, err
	}
	monitor, err := envBool("AGENT_MONITOR_ENABLED", true)
	if err != nil {
		return nil, err
	}
	notify, err := envBool("NOTIFICATIONS_ENABLED", false)
	if err != nil {
		return nil, err
	}
	retentionDays, err := envInt("INSIGHT_RETENTION_DAYS", 90)
	if err != nil {
		return nil, err
	}

	// API keys per cloud provider. The k8s manifests ship REPLACE_ME
	// placeholders so `kubectl apply` works without real keys; treat those
	// as "no key configured".
	apiKeys := map[string]string{}
	for provider, envName := range APIKeyEnvNames {
		if key := envStr(envName, ""); key != "" && key != "REPLACE_ME" {
			apiKeys[provider] = key
		}
	}

	cfg := &Agent{
		Port:           port,
		Kubeconfig:     envStr("KUBECONFIG_PATH", ""),
		LogLevel:       envStr("LOG_LEVEL", "info"),
		LogFormat:      envStr("LOG_FORMAT", "text"),
		Provider:       envStr("LLM_PROVIDER", "ollama"),
		Model:          envStr("LLM_MODEL", ""),
		APIKeys:        apiKeys,
		OllamaHost:     envStr("OLLAMA_HOST", "http://localhost:11434"),
		ReviewInterval: interval,
		MonitorEnabled: monitor,

		NotificationsEnabled: notify,
		DiscordWebhook:       envStr("DISCORD_WEBHOOK_URL", ""),
		SlackWebhook:         envStr("SLACK_WEBHOOK_URL", ""),
		TeamsWebhook:         envStr("TEAMS_WEBHOOK_URL", ""),
		NotifyMinSeverity:    envStr("NOTIFY_MIN_SEVERITY", "warning"),

		InsightDBPath:        envStr("INSIGHT_DB_PATH", ""),
		InsightRetentionDays: retentionDays,

		PrometheusURL: envStr("PROMETHEUS_URL", ""),
	}

	if cfg.NotifyMinSeverity != "warning" && cfg.NotifyMinSeverity != "critical" {
		return nil, fmt.Errorf("NOTIFY_MIN_SEVERITY %q is invalid (expected \"warning\" or \"critical\")", cfg.NotifyMinSeverity)
	}
	if cfg.NotificationsEnabled && cfg.DiscordWebhook == "" && cfg.SlackWebhook == "" && cfg.TeamsWebhook == "" {
		return nil, fmt.Errorf("NOTIFICATIONS_ENABLED=true requires at least one of DISCORD_WEBHOOK_URL, SLACK_WEBHOOK_URL, TEAMS_WEBHOOK_URL")
	}

	switch cfg.Provider {
	case "ollama":
		if cfg.Model == "" {
			cfg.Model = DefaultOllamaModel
		}
	case "anthropic", "openai", "deepseek", "gemini":
		if cfg.APIKeys[cfg.Provider] == "" {
			return nil, fmt.Errorf("LLM_PROVIDER=%s requires %s to be set", cfg.Provider, APIKeyEnvNames[cfg.Provider])
		}
		// Model defaults for cloud providers are filled by the agent runtime.
	default:
		return nil, fmt.Errorf("unsupported LLM_PROVIDER %q (expected one of: ollama, anthropic, openai, deepseek, gemini)", cfg.Provider)
	}

	if cfg.ReviewInterval < 30*time.Second {
		return nil, fmt.Errorf("AGENT_REVIEW_INTERVAL %s is too short (minimum 30s)", cfg.ReviewInterval)
	}
	return cfg, nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a valid integer", key, v)
	}
	return n, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a valid duration (e.g. \"5m\", \"90s\")", key, v)
	}
	return d, nil
}

func envBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %q is not a valid boolean", key, v)
	}
	return b, nil
}
