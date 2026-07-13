package agent

import (
	"log/slog"
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
	}, slog.Default())
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
		{Provider: "openai", OllamaHost: "x", ReviewInterval: "5m"},                 // unknown provider
		{Provider: "ollama", OllamaHost: "http://x", ReviewInterval: "5s"},          // too short
		{Provider: "ollama", OllamaHost: "http://x", ReviewInterval: "not-a-time"},  // bad duration
		{Provider: "ollama", OllamaHost: "", ReviewInterval: "5m"},                  // missing host
		{Provider: "anthropic", ReviewInterval: "5m"},                               // no key configured
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
