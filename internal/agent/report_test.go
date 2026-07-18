package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testReporter(t *testing.T, store *Store) (*Reporter, *Dispatcher) {
	t.Helper()
	rt := testRuntime(t)
	notifier := NewDispatcher(rt, slog.Default())
	return NewReporter(store, rt, notifier, slog.Default()), notifier
}

// flatten joins headline and all section lines for content assertions.
func flatten(headline string, sections []ReportSection) string {
	var b strings.Builder
	b.WriteString(headline + "\n")
	for _, s := range sections {
		b.WriteString(s.Title + "\n" + strings.Join(s.Lines, "\n") + "\n")
	}
	return b.String()
}

func TestReportBuildEmpty(t *testing.T) {
	rp, _ := testReporter(t, NewStore(20))
	status, headline, sections := rp.build()
	if status != StatusHealthy {
		t.Errorf("empty report status = %s, want healthy", status)
	}
	text := flatten(headline, sections)
	if !strings.Contains(text, "no reviews ran") || !strings.Contains(text, "None ran") {
		t.Errorf("empty report should say no reviews ran, got: %q", text)
	}
}

func TestReportBuildCountsAndChanges(t *testing.T) {
	dir := t.TempDir()
	store := NewPersistentStore(filepath.Join(dir, "insights.db"), 30, 20, slog.Default())
	defer store.Close()

	now := time.Now().UTC()
	for i, st := range []Status{StatusHealthy, StatusWarning, StatusCritical, StatusHealthy} {
		store.Add(Insight{
			Status:    st,
			Summary:   "review " + string(st),
			CreatedAt: now.Add(time.Duration(i-4) * time.Minute),
			Provider:  "ollama", Model: "qwen3",
		})
	}
	proposal, err := store.SaveProposal(Proposal{
		Kind: "deployments", Namespace: "shop", Name: "orders-api",
		Rationale:    "Fix the image tag.\nLong detail here.",
		ProposedYAML: "kind: Deployment",
	})
	if err != nil {
		t.Fatalf("SaveProposal: %v", err)
	}
	if err := store.ResolveProposal(proposal.ID, ""); err != nil {
		t.Fatalf("ResolveProposal: %v", err)
	}
	store.RecordUsage("ollama", "qwen3", "review", struct{ InputTokens, OutputTokens int }{1500, 300})

	rp, _ := testReporter(t, store)
	status, headline, sections := rp.build()
	text := flatten(headline, sections)

	if status != StatusHealthy {
		t.Errorf("status = %s, want healthy (latest review)", status)
	}
	for _, want := range []string{
		"4 reviews", "2 ✅ healthy · 1 ⚠️ warning · 1 ⛔ critical",
		"✅ applied · deployment shop/orders-api — Fix the image tag.",
		"1.5k in / 300 out",
		"free (local model)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report missing %q:\n%s", want, text)
		}
	}
	// healthy→warning→critical→healthy = 1 incident.
	if !strings.Contains(text, "1 time(s)") {
		t.Errorf("expected 1 incident, got:\n%s", text)
	}
	if strings.Contains(text, "Long detail here") {
		t.Errorf("rationale should be first line only:\n%s", text)
	}
	if strings.Contains(text, "**") {
		t.Errorf("report must not contain markdown (Slack renders it literally):\n%s", text)
	}
}

func TestReportSendRequiresWebhook(t *testing.T) {
	rp, _ := testReporter(t, NewStore(20))
	if err := rp.Send(context.Background()); err == nil {
		t.Fatal("Send without webhooks should error")
	}
}

func TestReportSendDelivers(t *testing.T) {
	rp, notifier := testReporter(t, NewStore(20))
	if _, err := rp.runtime.Apply(SettingsUpdate{
		Provider: "ollama", OllamaHost: "http://localhost:11434",
		ReviewInterval: "5m",
		SlackWebhook:   "https://hooks.slack.com/services/T00/B00/XXX",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var got Notification
	notifier.senders = map[string]func(context.Context, string, Notification) error{
		"slack": func(_ context.Context, _ string, n Notification) error { got = n; return nil },
	}
	if err := rp.Send(context.Background()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !got.Report {
		t.Error("delivered notification should have Report=true")
	}
	if len(got.Sections) == 0 {
		t.Error("delivered report should carry sections")
	}
	title, _ := embedStyle(got)
	if !strings.Contains(title, "daily report") {
		t.Errorf("report title = %q", title)
	}
}

func TestReportTimeValidation(t *testing.T) {
	rt := testRuntime(t)
	_, err := rt.Apply(SettingsUpdate{
		Provider: "ollama", OllamaHost: "http://localhost:11434",
		ReviewInterval: "5m", ReportTime: "25:99",
	})
	if err == nil || !strings.Contains(err.Error(), "report time") {
		t.Errorf("invalid report time should be rejected, got %v", err)
	}

	view, err := rt.Apply(SettingsUpdate{
		Provider: "ollama", OllamaHost: "http://localhost:11434",
		ReviewInterval: "5m", ReportTime: "17:30",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if view.ReportTime != "17:30" {
		t.Errorf("ReportTime = %q, want 17:30", view.ReportTime)
	}
	// Enabling the report without a webhook must fail (same rule as alerts).
	if _, err := rt.Apply(SettingsUpdate{
		Provider: "ollama", OllamaHost: "http://localhost:11434",
		ReviewInterval: "5m", ReportEnabled: true,
	}); err == nil {
		t.Error("report enabled without webhook should be rejected")
	}
}
