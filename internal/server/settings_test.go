package server

import (
	"context"
	"log/slog"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/emreoztoprak/kentinel/internal/k8s"
)

func TestPersistAgentConfig(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: agentConfigMapName, Namespace: "kentinel"},
			Data:       map[string]string{"LLM_PROVIDER": "ollama", "LOG_FORMAT": "json"},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: agentSecretName, Namespace: "kentinel"},
			Data:       map[string][]byte{"ANTHROPIC_API_KEY": []byte("REPLACE_ME")},
		},
	)
	srv := &Server{k8s: &k8s.Client{Clientset: clientset}, log: slog.Default()}

	err := srv.persistAgentConfig(context.Background(), "kentinel", agentConfigUpdate{
		Provider:       "anthropic",
		Model:          "claude-opus-4-8",
		OllamaHost:     "http://ollama:11434",
		APIKey:         "sk-ant-new",
		SlackWebhook:   "https://hooks.slack.com/services/T0/B0/xyz",
		ReviewInterval: "15m",
		MonitorEnabled: false,
	})
	if err != nil {
		t.Fatalf("persistAgentConfig failed: %v", err)
	}

	cm, _ := clientset.CoreV1().ConfigMaps("kentinel").Get(context.Background(), agentConfigMapName, metav1.GetOptions{})
	want := map[string]string{
		"LLM_PROVIDER":          "anthropic",
		"LLM_MODEL":             "claude-opus-4-8",
		"OLLAMA_HOST":           "http://ollama:11434",
		"AGENT_REVIEW_INTERVAL": "15m",
		"AGENT_MONITOR_ENABLED": "false",
	}
	for k, v := range want {
		if cm.Data[k] != v {
			t.Errorf("ConfigMap %s = %q, want %q", k, cm.Data[k], v)
		}
	}
	if cm.Data["LOG_FORMAT"] != "json" {
		t.Error("unrelated ConfigMap keys must be preserved")
	}

	secret, _ := clientset.CoreV1().Secrets("kentinel").Get(context.Background(), agentSecretName, metav1.GetOptions{})
	if string(secret.Data["ANTHROPIC_API_KEY"]) != "sk-ant-new" {
		t.Errorf("Secret key not updated: %q", secret.Data["ANTHROPIC_API_KEY"])
	}
	if string(secret.Data["SLACK_WEBHOOK_URL"]) != "https://hooks.slack.com/services/T0/B0/xyz" {
		t.Errorf("Slack webhook not persisted: %q", secret.Data["SLACK_WEBHOOK_URL"])
	}
}

func TestPersistAPIKeyGoesToProviderSpecificSecretKey(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: agentConfigMapName, Namespace: "ns"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: agentSecretName, Namespace: "ns"}},
	)
	srv := &Server{k8s: &k8s.Client{Clientset: clientset}, log: slog.Default()}

	err := srv.persistAgentConfig(context.Background(), "ns", agentConfigUpdate{
		Provider: "gemini", Model: "gemini-2.5-flash", OllamaHost: "http://x",
		APIKey: "AIza-test", ReviewInterval: "5m", MonitorEnabled: true,
	})
	if err != nil {
		t.Fatalf("persistAgentConfig failed: %v", err)
	}
	secret, _ := clientset.CoreV1().Secrets("ns").Get(context.Background(), agentSecretName, metav1.GetOptions{})
	if string(secret.Data["GEMINI_API_KEY"]) != "AIza-test" {
		t.Errorf("gemini key must land in GEMINI_API_KEY: %v", secret.Data)
	}
	if _, exists := secret.Data["ANTHROPIC_API_KEY"]; exists {
		t.Error("other providers' keys must not be touched")
	}
}

func TestPersistAgentConfigSkipsSecretWithoutKey(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: agentConfigMapName, Namespace: "ns"}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: agentSecretName, Namespace: "ns"},
			Data:       map[string][]byte{"ANTHROPIC_API_KEY": []byte("keep-me")},
		},
	)
	srv := &Server{k8s: &k8s.Client{Clientset: clientset}, log: slog.Default()}

	err := srv.persistAgentConfig(context.Background(), "ns", agentConfigUpdate{
		Provider: "ollama", Model: "qwen3", OllamaHost: "http://x", ReviewInterval: "5m", MonitorEnabled: true,
	})
	if err != nil {
		t.Fatalf("persistAgentConfig failed: %v", err)
	}

	secret, _ := clientset.CoreV1().Secrets("ns").Get(context.Background(), agentSecretName, metav1.GetOptions{})
	if string(secret.Data["ANTHROPIC_API_KEY"]) != "keep-me" {
		t.Error("secret must not change when no new key is provided")
	}
}
