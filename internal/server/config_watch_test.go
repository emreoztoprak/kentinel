package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testServer(t *testing.T, agentURL string) *Server {
	t.Helper()
	return New(nil, agentURL, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestPushConfigToAgentBuildsCorrectPayload(t *testing.T) {
	var captured settingsSyncPayload
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/sync" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decoding pushed payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer agent.Close()

	s := testServer(t, agent.URL)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-config"},
		Data: map[string]string{
			"LLM_PROVIDER":          "anthropic",
			"LLM_MODEL":             "claude-opus-4-8",
			"AGENT_REVIEW_INTERVAL": "15m",
			"AGENT_MONITOR_ENABLED": "true",
			"NOTIFICATIONS_ENABLED": "true",
			"NOTIFY_MIN_SEVERITY":   "critical",
			"PROMETHEUS_URL":        "http://prometheus:9090",
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-secrets"},
		Data: map[string][]byte{
			"ANTHROPIC_API_KEY":   []byte("sk-ant-real"),
			"OPENAI_API_KEY":      []byte("REPLACE_ME"), // must be treated as unconfigured
			"SLACK_WEBHOOK_URL":   []byte("https://hooks.slack.com/services/T/B/x"),
			"DISCORD_WEBHOOK_URL": []byte(""),
		},
	}

	if err := s.pushConfigToAgent(t.Context(), cm, secret); err != nil {
		t.Fatalf("pushConfigToAgent failed: %v", err)
	}

	if captured.Provider != "anthropic" || captured.Model != "claude-opus-4-8" {
		t.Errorf("provider/model = %q/%q", captured.Provider, captured.Model)
	}
	if captured.ReviewInterval != "15m" || !captured.MonitorEnabled {
		t.Errorf("reviewInterval/monitor = %q/%v", captured.ReviewInterval, captured.MonitorEnabled)
	}
	if !captured.NotificationsEnabled || captured.NotifyMinSeverity != "critical" {
		t.Errorf("notifications = %v/%q", captured.NotificationsEnabled, captured.NotifyMinSeverity)
	}
	if captured.APIKeys["anthropic"] != "sk-ant-real" {
		t.Errorf("anthropic key = %q, want the real value", captured.APIKeys["anthropic"])
	}
	if _, ok := captured.APIKeys["openai"]; ok {
		t.Error("REPLACE_ME placeholder must not be forwarded as a real key")
	}
	if captured.SlackWebhook != "https://hooks.slack.com/services/T/B/x" {
		t.Errorf("slackWebhook = %q", captured.SlackWebhook)
	}
	if captured.DiscordWebhook != "" {
		t.Errorf("discordWebhook = %q, want empty (authoritative: absent means unset)", captured.DiscordWebhook)
	}
}

func TestPushConfigToAgentSurfacesAgentRejection(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad_request","message":"ollamaHost is required"}`))
	}))
	defer agent.Close()

	s := testServer(t, agent.URL)
	cm := &corev1.ConfigMap{Data: map[string]string{"LLM_PROVIDER": "ollama"}}
	secret := &corev1.Secret{Data: map[string][]byte{}}

	err := s.pushConfigToAgent(t.Context(), cm, secret)
	if err == nil {
		t.Fatal("expected an error when the agent rejects the sync")
	}
}
