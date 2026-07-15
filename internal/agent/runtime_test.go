package agent

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/emreoztoprak/kentinel/internal/config"
)

func testRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt, err := NewRuntime(&config.Agent{
		Provider:       "ollama",
		Model:          "qwen3",
		OllamaHost:     "http://localhost:11434",
		ReviewInterval: 5 * time.Minute,
		MonitorEnabled: true,
	}, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	return rt
}

func TestApplyValidUpdate(t *testing.T) {
	rt := testRuntime(t)

	view, err := rt.Apply(SettingsUpdate{
		Provider:       "ollama",
		Model:          "llama3.1",
		OllamaHost:     "http://ollama:11434",
		ReviewInterval: "10m",
		MonitorEnabled: false,
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if view.Model != "llama3.1" || view.ReviewInterval != "10m0s" || view.MonitorEnabled {
		t.Errorf("unexpected view: %+v", view)
	}
	if rt.Provider().Model() != "llama3.1" {
		t.Errorf("provider not swapped: %s", rt.Provider().Model())
	}

	// A change signal must be pending for the monitor loop.
	select {
	case <-rt.Changed():
	default:
		t.Error("Apply did not signal Changed()")
	}
}

func TestApplyFillsProviderDefaultModel(t *testing.T) {
	rt := testRuntime(t)
	view, err := rt.Apply(SettingsUpdate{
		Provider:       "ollama",
		Model:          "",
		OllamaHost:     "http://localhost:11434",
		ReviewInterval: "5m",
		MonitorEnabled: true,
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if view.Model != config.DefaultOllamaModel {
		t.Errorf("model = %q, want provider default %q", view.Model, config.DefaultOllamaModel)
	}
}

func TestApplyRejectsInvalidUpdates(t *testing.T) {
	rt := testRuntime(t)
	cases := []SettingsUpdate{
		{Provider: "openai", OllamaHost: "x", ReviewInterval: "5m"},                // unknown provider
		{Provider: "ollama", OllamaHost: "http://x", ReviewInterval: "5s"},         // too short
		{Provider: "ollama", OllamaHost: "http://x", ReviewInterval: "not-a-time"}, // bad duration
		{Provider: "ollama", OllamaHost: "", ReviewInterval: "5m"},                 // missing host
		{Provider: "anthropic", ReviewInterval: "5m"},                              // no key configured
	}
	for i, update := range cases {
		if _, err := rt.Apply(update); err == nil {
			t.Errorf("case %d: Apply(%+v) should fail", i, update)
		}
	}
	// State must be unchanged after failed applies.
	if view := rt.View(); view.Provider != "ollama" || view.ReviewInterval != "5m0s" {
		t.Errorf("failed applies mutated state: %+v", view)
	}
}

func TestApplyKeysPerProviderAndSwitching(t *testing.T) {
	rt := testRuntime(t)

	// The apiKey field applies to the provider selected in the same update:
	// switching to anthropic while providing its key.
	view, err := rt.Apply(SettingsUpdate{
		Provider: "anthropic", OllamaHost: "http://x", ReviewInterval: "5m",
		APIKey: "sk-ant-test", MonitorEnabled: true,
	})
	if err != nil {
		t.Fatalf("Apply with key failed: %v", err)
	}
	if !view.APIKeysSet["anthropic"] || view.APIKeysSet["openai"] {
		t.Fatalf("apiKeysSet = %+v", view.APIKeysSet)
	}
	if view.Model != config.DefaultAnthropicModel {
		t.Errorf("model = %q, want anthropic default", view.Model)
	}

	// Cloud provider without any key must fail.
	if _, err := rt.Apply(SettingsUpdate{
		Provider: "openai", OllamaHost: "http://x", ReviewInterval: "5m", MonitorEnabled: true,
	}); err == nil {
		t.Fatal("switching to openai without a key should fail")
	}

	// Provide the openai key; each provider gets its own default model.
	view, err = rt.Apply(SettingsUpdate{
		Provider: "openai", OllamaHost: "http://x", ReviewInterval: "5m",
		APIKey: "sk-openai-test", MonitorEnabled: true,
	})
	if err != nil {
		t.Fatalf("switching to openai with key failed: %v", err)
	}
	if view.Provider != "openai" || view.Model == "" || view.Model == config.DefaultAnthropicModel {
		t.Errorf("unexpected view after switch: %+v", view)
	}
	if !view.APIKeysSet["anthropic"] {
		t.Error("anthropic key must survive switching providers")
	}

	// Switching back to anthropic without re-sending its key must work.
	if _, err := rt.Apply(SettingsUpdate{
		Provider: "anthropic", OllamaHost: "http://x", ReviewInterval: "5m", MonitorEnabled: true,
	}); err != nil {
		t.Fatalf("switching back with stored key failed: %v", err)
	}
}

func TestDeepseekAndGeminiProviders(t *testing.T) {
	rt := testRuntime(t)
	for _, provider := range []string{"deepseek", "gemini"} {
		view, err := rt.Apply(SettingsUpdate{
			Provider: provider, OllamaHost: "http://x", ReviewInterval: "5m",
			APIKey: "test-key-" + provider, MonitorEnabled: true,
		})
		if err != nil {
			t.Fatalf("Apply(%s) failed: %v", provider, err)
		}
		if view.Provider != provider || view.Model == "" {
			t.Errorf("%s view = %+v", provider, view)
		}
		if rt.Provider().Name() != provider {
			t.Errorf("provider name = %s, want %s", rt.Provider().Name(), provider)
		}
	}
}

func baseConfig() *config.Agent {
	return &config.Agent{
		Provider:       "ollama",
		Model:          "qwen3",
		OllamaHost:     "http://localhost:11434",
		ReviewInterval: 5 * time.Minute,
		MonitorEnabled: true,
	}
}

// TestApplyPersistsAndRestoresAcrossRestart verifies the whole point of the
// settings store: a saved change outlives the process, and the next
// Runtime built against the same database restores it instead of falling
// back to deployment defaults.
func TestApplyPersistsAndRestoresAcrossRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	store := NewPersistentStore(dbPath, 90, 20, slog.Default())
	if !store.SettingsPersistent() {
		t.Fatal("expected settings persistence to be available")
	}

	rt, err := NewRuntime(baseConfig(), store, slog.Default())
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	view, err := rt.Apply(SettingsUpdate{
		Provider: "anthropic", OllamaHost: "http://x", ReviewInterval: "15m",
		APIKey: "sk-ant-test", MonitorEnabled: false,
	})
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if !view.Persistent {
		t.Error("view.Persistent should be true with a working store")
	}

	// A fresh Runtime against the SAME store simulates a pod restart: the
	// persisted settings must win over the (unrelated) deployment defaults
	// passed via cfg.
	restarted, err := NewRuntime(baseConfig(), store, slog.Default())
	if err != nil {
		t.Fatalf("NewRuntime (restart) failed: %v", err)
	}
	got := restarted.View()
	if got.Provider != "anthropic" || got.ReviewInterval != "15m0s" || got.MonitorEnabled {
		t.Errorf("settings did not survive restart: %+v", got)
	}
	if !got.APIKeysSet["anthropic"] {
		t.Error("anthropic API key did not survive restart")
	}
	if restarted.Provider().Name() != "anthropic" {
		t.Errorf("restored provider = %s, want anthropic", restarted.Provider().Name())
	}
}

// TestNewRuntimeFallsBackWhenPersistedSettingsAreInvalid confirms a stale
// or corrupted saved value never keeps the agent from booting — it must
// fall back to the deployment defaults instead of failing NewRuntime.
func TestNewRuntimeFallsBackWhenPersistedSettingsAreInvalid(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	store := NewPersistentStore(dbPath, 90, 20, slog.Default())

	// Save a provider that requires a key it doesn't have — buildProvider
	// will reject it, simulating a stale/corrupted persisted value.
	store.SaveSettings(Settings{
		Provider: "anthropic", Model: "claude-opus-4-8",
		ReviewInterval: 5 * time.Minute, MonitorEnabled: true,
		NotifyMinSeverity: "warning",
	})

	rt, err := NewRuntime(baseConfig(), store, slog.Default())
	if err != nil {
		t.Fatalf("NewRuntime should fall back to deployment defaults, not fail: %v", err)
	}
	if rt.View().Provider != "ollama" {
		t.Errorf("provider = %q, want fallback to deployment default \"ollama\"", rt.View().Provider)
	}
}

// TestSyncIsAuthoritative verifies Sync's key difference from Apply: an
// empty field really does clear a previously-set value, matching a
// Kubernetes ConfigMap/Secret's actual current (not write-only) contents.
func TestSyncIsAuthoritative(t *testing.T) {
	rt := testRuntime(t)

	if _, err := rt.Apply(SettingsUpdate{
		Provider: "ollama", OllamaHost: "http://x", ReviewInterval: "5m",
		MonitorEnabled: true, NotificationsEnabled: true,
		SlackWebhook: "https://hooks.slack.com/services/T/B/x",
	}); err != nil {
		t.Fatalf("seeding webhook via Apply failed: %v", err)
	}
	if !rt.View().SlackWebhookSet {
		t.Fatal("webhook was not set by Apply")
	}

	// Sync with an empty SlackWebhook and NotificationsEnabled=false must
	// actually clear it — this is exactly what happens when a `helm
	// upgrade` removes a previously-set webhook from the Secret.
	view, err := rt.Sync(Settings{
		Provider: "ollama", OllamaHost: "http://x", ReviewInterval: 5 * time.Minute,
		MonitorEnabled: true, NotifyMinSeverity: "warning",
	})
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if view.SlackWebhookSet {
		t.Error("Sync should have cleared the webhook, Apply's write-only semantics must not apply")
	}
	if view.NotificationsEnabled {
		t.Error("Sync should have disabled notifications")
	}
}

// TestSyncDefaultsEmptyNotifyMinSeverity mirrors merge()'s behavior: a
// ConfigMap missing NOTIFY_MIN_SEVERITY (empty string) must default to
// "warning" rather than fail validation.
func TestSyncDefaultsEmptyNotifyMinSeverity(t *testing.T) {
	rt := testRuntime(t)
	view, err := rt.Sync(Settings{
		Provider: "ollama", OllamaHost: "http://x", ReviewInterval: 5 * time.Minute,
		MonitorEnabled: true,
	})
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if view.NotifyMinSeverity != "warning" {
		t.Errorf("NotifyMinSeverity = %q, want default \"warning\"", view.NotifyMinSeverity)
	}
}

// TestSyncRejectsInvalidSettings confirms Sync shares validateSettings with
// Apply rather than skipping validation for an "authoritative" source.
func TestSyncRejectsInvalidSettings(t *testing.T) {
	rt := testRuntime(t)
	_, err := rt.Sync(Settings{
		Provider: "ollama", OllamaHost: "", ReviewInterval: 5 * time.Minute, MonitorEnabled: true,
	})
	if err == nil {
		t.Fatal("Sync with a missing ollamaHost should fail validation")
	}
}
