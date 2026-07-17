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

	// Persistent reports whether this settings state survives a pod
	// restart (a working SQLite file + encryption key). false in Docker
	// mode without INSIGHT_DB_PATH, or if the database/key couldn't be
	// opened — the change still applies live either way.
	Persistent bool `json:"persistent"`
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
	store    *Store        // nil when settings persistence isn't available
	log      *slog.Logger
}

// NewRuntime builds the runtime from boot config (deployment defaults from
// env vars — used only when nothing has ever been saved yet), then checks
// store for settings saved by a previous Apply. If any exist, they replace
// cfg entirely and cfg is never consulted again — the ConfigMap/Secret only
// matter on a genuinely first boot (fresh install, or the database was
// lost). store may be nil (Docker mode without persistence); NewRuntime
// falls back to cfg alone in that case. Fails only if neither the persisted
// nor the deployment-default provider can be built.
func NewRuntime(cfg *config.Agent, store *Store, log *slog.Logger) (*Runtime, error) {
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

	if store != nil {
		if persisted, ok, err := store.LoadSettings(); err != nil {
			log.Warn("loading persisted settings failed; using deployment defaults", "error", err)
		} else if ok {
			// Validate before committing to the persisted state: a stale or
			// corrupted saved value (e.g. a since-revoked provider key)
			// must never keep the agent from booting — same principle as
			// the store's own persistence failures being non-fatal.
			if _, err := buildProvider(persisted); err != nil {
				log.Warn("persisted settings are invalid; using deployment defaults", "error", err)
			} else {
				settings = persisted
				log.Info("restored settings from database", "provider", settings.Provider, "model", settings.Model)
			}
		}
	}

	provider, err := buildProvider(settings)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		settings: settings,
		provider: provider,
		changed:  make(chan struct{}, 1),
		store:    store,
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
		Persistent:    r.store != nil && r.store.SettingsPersistent(),
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

	if r.store != nil {
		r.store.SaveSettings(next)
	}

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

	next.PrometheusURL = strings.TrimRight(update.PrometheusURL, "/")

	next.Model = update.Model
	if next.Model == "" {
		next.Model = DefaultModel(update.Provider)
	}

	if err := validateSettings(next); err != nil {
		return Settings{}, err
	}
	return next, nil
}

// validateSettings checks a fully-merged settings value, split out of
// merge() for readability.
func validateSettings(s Settings) error {
	if s.ReviewInterval < 30*time.Second {
		return fmt.Errorf("reviewInterval %s is too short (minimum 30s)", s.ReviewInterval)
	}

	switch {
	case s.Provider == "ollama":
		if s.OllamaHost == "" {
			return fmt.Errorf("provider \"ollama\" requires ollamaHost")
		}
	case isCloudProvider(s.Provider):
		if s.APIKeys[s.Provider] == "" {
			return fmt.Errorf("provider %q requires an API key (none is configured yet)", s.Provider)
		}
	default:
		return fmt.Errorf("unsupported provider %q (expected one of: ollama, %s)", s.Provider, strings.Join(CloudProviders, ", "))
	}

	if s.DiscordWebhook != "" && !isDiscordWebhook(s.DiscordWebhook) {
		return fmt.Errorf("discordWebhookUrl must be a Discord webhook URL (https://discord.com/api/webhooks/...)")
	}
	if s.SlackWebhook != "" && !strings.HasPrefix(s.SlackWebhook, "https://hooks.slack.com/") {
		return fmt.Errorf("slackWebhookUrl must be a Slack incoming-webhook URL (https://hooks.slack.com/...)")
	}
	if s.TeamsWebhook != "" && !strings.HasPrefix(s.TeamsWebhook, "https://") {
		return fmt.Errorf("teamsWebhookUrl must be an https URL")
	}
	switch s.NotifyMinSeverity {
	case "warning", "critical":
	default:
		return fmt.Errorf("notifyMinSeverity %q is invalid (expected \"warning\" or \"critical\")", s.NotifyMinSeverity)
	}
	if s.PrometheusURL != "" &&
		!strings.HasPrefix(s.PrometheusURL, "http://") &&
		!strings.HasPrefix(s.PrometheusURL, "https://") {
		return fmt.Errorf("prometheusUrl must be an http(s) URL (or empty to disable metrics)")
	}
	if s.NotificationsEnabled && s.DiscordWebhook == "" && s.SlackWebhook == "" && s.TeamsWebhook == "" {
		return fmt.Errorf("enabling notifications requires at least one webhook URL (Discord, Slack, or Teams)")
	}
	return nil
}

// pickOllamaModel chooses which Ollama model to use given a preferred name
// and the set of models actually installed on the server. It returns the
// preferred model when it's installed (or when the installed set is unknown,
// e.g. the server was unreachable), otherwise the first installed model —
// so "provider default" always resolves to something the server can run.
func pickOllamaModel(preferred string, installed []string) string {
	if len(installed) == 0 {
		return preferred
	}
	for _, m := range installed {
		if m == preferred {
			return preferred
		}
	}
	return installed[0]
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
