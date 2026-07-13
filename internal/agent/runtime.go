package agent

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emreoztoprak/kentinel/internal/config"
	"github.com/emreoztoprak/kentinel/internal/llm"
	anthropicllm "github.com/emreoztoprak/kentinel/internal/llm/anthropic"
	ollamallm "github.com/emreoztoprak/kentinel/internal/llm/ollama"
	"github.com/emreoztoprak/kentinel/internal/llm/openaicompat"
)

// CloudProviders are the providers that require an API key, in display
// order. "ollama" is the only keyless provider.
var CloudProviders = []string{"anthropic", "openai", "deepseek", "gemini"}

// Settings are the agent parameters that can be changed at runtime from the
// UI. API keys and webhook URLs are stored but never exposed (see View).
type Settings struct {
	Provider       string
	Model          string
	OllamaHost     string
	APIKeys        map[string]string // provider -> key (cloud providers only)
	ReviewInterval time.Duration
	MonitorEnabled bool

	NotificationsEnabled bool
	DiscordWebhook       string
	SlackWebhook         string
	TeamsWebhook         string
	NotifyMinSeverity    string // "warning" or "critical"

	PrometheusURL string // empty = metrics tools disabled
}

// SettingsView is the safe, serializable form of Settings — secrets are
// reduced to booleans.
type SettingsView struct {
	Provider       string          `json:"provider"`
	Model          string          `json:"model"`
	OllamaHost     string          `json:"ollamaHost"`
	APIKeysSet     map[string]bool `json:"apiKeysSet"` // provider -> key configured?
	ReviewInterval string          `json:"reviewInterval"`
	MonitorEnabled bool            `json:"monitorEnabled"`

	NotificationsEnabled bool   `json:"notificationsEnabled"`
	DiscordWebhookSet    bool   `json:"discordWebhookSet"`
	SlackWebhookSet      bool   `json:"slackWebhookSet"`
	TeamsWebhookSet      bool   `json:"teamsWebhookSet"`
	NotifyMinSeverity    string `json:"notifyMinSeverity"`

	PrometheusURL string `json:"prometheusUrl"`
}

// SettingsUpdate is the PUT /config payload. Secret fields are write-only:
// empty means "keep the existing value". APIKey applies to the provider
// selected in the same update.
type SettingsUpdate struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	OllamaHost     string `json:"ollamaHost"`
	APIKey         string `json:"apiKey,omitempty"`
	ReviewInterval string `json:"reviewInterval"`
	MonitorEnabled bool   `json:"monitorEnabled"`

	NotificationsEnabled bool   `json:"notificationsEnabled"`
	DiscordWebhook       string `json:"discordWebhookUrl,omitempty"`
	SlackWebhook         string `json:"slackWebhookUrl,omitempty"`
	TeamsWebhook         string `json:"teamsWebhookUrl,omitempty"`
	NotifyMinSeverity    string `json:"notifyMinSeverity"`

	// PrometheusURL is plain (not write-only): empty string DISABLES metrics.
	PrometheusURL string `json:"prometheusUrl"`
}

// Runtime holds the agent's mutable configuration and the active LLM
// provider. All access is through accessors so settings can change while
// reviews and queries are running (in-flight calls keep the provider they
// started with).
type Runtime struct {
	mu       sync.RWMutex
	settings Settings
	provider llm.Provider
	changed  chan struct{} // signals the monitor loop after Apply
	log      *slog.Logger
}

// NewRuntime builds the runtime from boot config. Fails if the initial
// provider cannot be constructed.
func NewRuntime(cfg *config.Agent, log *slog.Logger) (*Runtime, error) {
	settings := Settings{
		Provider:       cfg.Provider,
		Model:          cfg.Model,
		OllamaHost:     cfg.OllamaHost,
		APIKeys:        cloneKeys(cfg.APIKeys),
		ReviewInterval: cfg.ReviewInterval,
		MonitorEnabled: cfg.MonitorEnabled,

		NotificationsEnabled: cfg.NotificationsEnabled,
		DiscordWebhook:       cfg.DiscordWebhook,
		SlackWebhook:         cfg.SlackWebhook,
		TeamsWebhook:         cfg.TeamsWebhook,
		NotifyMinSeverity:    cfg.NotifyMinSeverity,

		PrometheusURL: cfg.PrometheusURL,
	}
	if settings.Model == "" {
		settings.Model = DefaultModel(settings.Provider)
	}
	provider, err := buildProvider(settings)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		settings: settings,
		provider: provider,
		changed:  make(chan struct{}, 1),
		log:      log,
	}, nil
}

// Provider returns the active LLM provider.
func (r *Runtime) Provider() llm.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.provider
}

// View returns the current settings with all secrets masked.
func (r *Runtime) View() SettingsView {
	r.mu.RLock()
	defer r.mu.RUnlock()

	keysSet := make(map[string]bool, len(CloudProviders))
	for _, p := range CloudProviders {
		keysSet[p] = r.settings.APIKeys[p] != ""
	}

	return SettingsView{
		Provider:       r.settings.Provider,
		Model:          r.settings.Model,
		OllamaHost:     r.settings.OllamaHost,
		APIKeysSet:     keysSet,
		ReviewInterval: r.settings.ReviewInterval.String(),
		MonitorEnabled: r.settings.MonitorEnabled,

		NotificationsEnabled: r.settings.NotificationsEnabled,
		DiscordWebhookSet:    r.settings.DiscordWebhook != "",
		SlackWebhookSet:      r.settings.SlackWebhook != "",
		TeamsWebhookSet:      r.settings.TeamsWebhook != "",
		NotifyMinSeverity:    r.settings.NotifyMinSeverity,

		PrometheusURL: r.settings.PrometheusURL,
	}
}

// MonitorSettings returns what the review loop needs each iteration.
func (r *Runtime) MonitorSettings() (enabled bool, interval time.Duration) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.settings.MonitorEnabled, r.settings.ReviewInterval
}

// NotificationChannel is one configured webhook destination.
type NotificationChannel struct {
	Name string // "discord", "slack", "teams"
	URL  string
}

// NotificationSettings returns the enabled flag, all configured channels,
// and the minimum severity.
func (r *Runtime) NotificationSettings() (enabled bool, channels []NotificationChannel, minSeverity string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.settings.DiscordWebhook != "" {
		channels = append(channels, NotificationChannel{Name: "discord", URL: r.settings.DiscordWebhook})
	}
	if r.settings.SlackWebhook != "" {
		channels = append(channels, NotificationChannel{Name: "slack", URL: r.settings.SlackWebhook})
	}
	if r.settings.TeamsWebhook != "" {
		channels = append(channels, NotificationChannel{Name: "teams", URL: r.settings.TeamsWebhook})
	}
	return r.settings.NotificationsEnabled, channels, r.settings.NotifyMinSeverity
}

// Prometheus returns a client for the configured Prometheus, or nil when
// metrics are disabled.
func (r *Runtime) Prometheus() *promClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.settings.PrometheusURL == "" {
		return nil
	}
	return newPromClient(r.settings.PrometheusURL)
}

// Changed delivers a signal after every successful Apply so the monitor loop
// can re-read its settings (and review immediately with the new provider).
func (r *Runtime) Changed() <-chan struct{} { return r.changed }

// Apply validates and activates a settings update. On success the new
// provider is live for all subsequent reviews and queries.
func (r *Runtime) Apply(update SettingsUpdate) (SettingsView, error) {
	next, err := r.merge(update)
	if err != nil {
		return SettingsView{}, err
	}

	provider, err := buildProvider(next)
	if err != nil {
		return SettingsView{}, err
	}

	r.mu.Lock()
	r.settings = next
	r.provider = provider
	r.mu.Unlock()

	r.log.Info("settings updated",
		"provider", next.Provider, "model", next.Model,
		"interval", next.ReviewInterval.String(), "monitor", next.MonitorEnabled)

	select {
	case r.changed <- struct{}{}:
	default: // a signal is already pending
	}
	return r.View(), nil
}

// merge validates the update against current state and produces the next
// settings. Write-only secrets are never dropped unless replaced.
func (r *Runtime) merge(update SettingsUpdate) (Settings, error) {
	r.mu.RLock()
	next := r.settings
	next.APIKeys = cloneKeys(r.settings.APIKeys)
	r.mu.RUnlock()

	interval, err := time.ParseDuration(update.ReviewInterval)
	if err != nil {
		return Settings{}, fmt.Errorf("reviewInterval %q is not a valid duration (e.g. \"5m\", \"90s\")", update.ReviewInterval)
	}
	if interval < 30*time.Second {
		return Settings{}, fmt.Errorf("reviewInterval %s is too short (minimum 30s)", interval)
	}

	// Provider + key validation.
	switch {
	case update.Provider == "ollama":
		if update.OllamaHost == "" {
			return Settings{}, fmt.Errorf("provider \"ollama\" requires ollamaHost")
		}
	case isCloudProvider(update.Provider):
		if update.APIKey == "" && next.APIKeys[update.Provider] == "" {
			return Settings{}, fmt.Errorf("provider %q requires an API key (none is configured yet)", update.Provider)
		}
	default:
		return Settings{}, fmt.Errorf("unsupported provider %q (expected one of: ollama, %s)", update.Provider, strings.Join(CloudProviders, ", "))
	}

	// Notification validation.
	if update.DiscordWebhook != "" && !isDiscordWebhook(update.DiscordWebhook) {
		return Settings{}, fmt.Errorf("discordWebhookUrl must be a Discord webhook URL (https://discord.com/api/webhooks/...)")
	}
	if update.SlackWebhook != "" && !strings.HasPrefix(update.SlackWebhook, "https://hooks.slack.com/") {
		return Settings{}, fmt.Errorf("slackWebhookUrl must be a Slack incoming-webhook URL (https://hooks.slack.com/...)")
	}
	if update.TeamsWebhook != "" && !strings.HasPrefix(update.TeamsWebhook, "https://") {
		return Settings{}, fmt.Errorf("teamsWebhookUrl must be an https URL")
	}
	switch update.NotifyMinSeverity {
	case "", "warning", "critical":
	default:
		return Settings{}, fmt.Errorf("notifyMinSeverity %q is invalid (expected \"warning\" or \"critical\")", update.NotifyMinSeverity)
	}

	if update.PrometheusURL != "" &&
		!strings.HasPrefix(update.PrometheusURL, "http://") &&
		!strings.HasPrefix(update.PrometheusURL, "https://") {
		return Settings{}, fmt.Errorf("prometheusUrl must be an http(s) URL (or empty to disable metrics)")
	}

	// Merge.
	next.Provider = update.Provider
	next.OllamaHost = update.OllamaHost
	next.ReviewInterval = interval
	next.MonitorEnabled = update.MonitorEnabled
	if update.APIKey != "" && isCloudProvider(update.Provider) {
		next.APIKeys[update.Provider] = update.APIKey
	}

	next.NotificationsEnabled = update.NotificationsEnabled
	if update.DiscordWebhook != "" {
		next.DiscordWebhook = update.DiscordWebhook
	}
	if update.SlackWebhook != "" {
		next.SlackWebhook = update.SlackWebhook
	}
	if update.TeamsWebhook != "" {
		next.TeamsWebhook = update.TeamsWebhook
	}
	next.NotifyMinSeverity = update.NotifyMinSeverity
	if next.NotifyMinSeverity == "" {
		next.NotifyMinSeverity = "warning"
	}
	if update.NotificationsEnabled &&
		next.DiscordWebhook == "" && next.SlackWebhook == "" && next.TeamsWebhook == "" {
		return Settings{}, fmt.Errorf("enabling notifications requires at least one webhook URL (Discord, Slack, or Teams)")
	}

	next.PrometheusURL = strings.TrimRight(update.PrometheusURL, "/")

	next.Model = update.Model
	if next.Model == "" {
		next.Model = DefaultModel(update.Provider)
	}
	return next, nil
}

// DefaultModel returns the default model ID for a provider.
func DefaultModel(provider string) string {
	switch provider {
	case "anthropic":
		return config.DefaultAnthropicModel
	case "ollama":
		return config.DefaultOllamaModel
	default:
		if preset, ok := openaicompat.Presets[provider]; ok {
			return preset.DefaultModel
		}
		return ""
	}
}

// KnownModels returns the curated (or, for ollama, caller-supplied) model
// list for a provider. Returns nil for unknown providers.
func KnownModels(provider string) []string {
	switch provider {
	case "anthropic":
		return anthropicllm.KnownModels()
	default:
		if preset, ok := openaicompat.Presets[provider]; ok {
			return append([]string(nil), preset.KnownModels...)
		}
		return nil
	}
}

func isCloudProvider(name string) bool {
	for _, p := range CloudProviders {
		if p == name {
			return true
		}
	}
	return false
}

// buildProvider constructs the LLM provider for the given settings.
func buildProvider(s Settings) (llm.Provider, error) {
	switch s.Provider {
	case "anthropic":
		if s.APIKeys["anthropic"] == "" {
			return nil, fmt.Errorf("anthropic provider requires an API key")
		}
		return anthropicllm.New(s.APIKeys["anthropic"], s.Model), nil
	case "ollama":
		return ollamallm.New(s.OllamaHost, s.Model), nil
	default:
		preset, ok := openaicompat.Presets[s.Provider]
		if !ok {
			keys := make([]string, 0, len(openaicompat.Presets))
			for k := range openaicompat.Presets {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return nil, fmt.Errorf("unsupported provider %q (openai-compatible presets: %s)", s.Provider, strings.Join(keys, ", "))
		}
		if s.APIKeys[s.Provider] == "" {
			return nil, fmt.Errorf("%s provider requires an API key", s.Provider)
		}
		return openaicompat.New(preset.Name, preset.BaseURL, s.APIKeys[s.Provider], s.Model), nil
	}
}

func cloneKeys(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
