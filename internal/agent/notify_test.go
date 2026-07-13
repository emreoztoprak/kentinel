package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func notifyRuntime(t *testing.T, enabled bool, minSeverity string) *Runtime {
	t.Helper()
	return &Runtime{
		settings: Settings{
			Provider: "ollama", Model: "m", OllamaHost: "http://x",
			ReviewInterval:       time.Minute,
			NotificationsEnabled: enabled,
			DiscordWebhook:       "https://discord.com/api/webhooks/1/abc",
			NotifyMinSeverity:    minSeverity,
		},
		changed: make(chan struct{}, 1),
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// recordingDispatcher returns a dispatcher whose sends are captured (all
// channels record into the same slice).
func recordingDispatcher(rt *Runtime) (*Dispatcher, *[]Notification) {
	var sent []Notification
	d := NewDispatcher(rt, slog.New(slog.NewTextHandler(io.Discard, nil)))
	record := func(_ context.Context, _ string, n Notification) error {
		sent = append(sent, n)
		return nil
	}
	for name := range d.senders {
		d.senders[name] = record
	}
	return d, &sent
}

func TestDispatcherNotifiesOnTransitionsOnly(t *testing.T) {
	d, sent := recordingDispatcher(notifyRuntime(t, true, "warning"))
	ctx := context.Background()

	steps := []struct {
		status    Status
		wantSends int // cumulative
	}{
		{StatusHealthy, 0},  // first review healthy: below threshold, silent
		{StatusHealthy, 0},  // no transition
		{StatusWarning, 1},  // healthy → warning: alert
		{StatusWarning, 1},  // still warning: silent (dedup)
		{StatusCritical, 2}, // warning → critical: escalation
		{StatusError, 2},    // review failure: never notifies
		{StatusCritical, 2}, // unchanged after the error gap
		{StatusHealthy, 3},  // critical → healthy: recovery
	}
	for i, step := range steps {
		d.Process(ctx, Insight{Status: step.status, Summary: "s"})
		if len(*sent) != step.wantSends {
			t.Fatalf("step %d (%s): sends = %d, want %d", i, step.status, len(*sent), step.wantSends)
		}
	}

	// Recovery notification must carry the previous status.
	last := (*sent)[len(*sent)-1]
	if last.Status != StatusHealthy || last.Previous != StatusCritical {
		t.Errorf("recovery notification = %+v", last)
	}
}

func TestDispatcherRespectsCriticalThreshold(t *testing.T) {
	d, sent := recordingDispatcher(notifyRuntime(t, true, "critical"))
	ctx := context.Background()

	d.Process(ctx, Insight{Status: StatusWarning, Summary: "s"})  // below threshold
	d.Process(ctx, Insight{Status: StatusHealthy, Summary: "s"})  // warning→healthy, both below
	d.Process(ctx, Insight{Status: StatusCritical, Summary: "s"}) // alert
	d.Process(ctx, Insight{Status: StatusWarning, Summary: "s"})  // critical→warning: recovery-ish, prev qualifies
	if len(*sent) != 2 {
		t.Fatalf("sends = %d, want 2 (critical alert + downgrade)", len(*sent))
	}
}

func TestDispatcherSilentWhenDisabledButTracksState(t *testing.T) {
	rt := notifyRuntime(t, false, "warning")
	d, sent := recordingDispatcher(rt)
	ctx := context.Background()

	d.Process(ctx, Insight{Status: StatusCritical, Summary: "s"})
	if len(*sent) != 0 {
		t.Fatal("disabled dispatcher must not send")
	}

	// Enable notifications; the next review has the SAME status — no stale
	// transition may fire.
	rt.mu.Lock()
	rt.settings.NotificationsEnabled = true
	rt.mu.Unlock()
	d.Process(ctx, Insight{Status: StatusCritical, Summary: "s"})
	if len(*sent) != 0 {
		t.Fatal("no transition after enabling — must stay silent")
	}
}

func TestSendDiscordPayloadAndErrors(t *testing.T) {
	var captured discordPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decoding payload: %v", err)
		}
		w.WriteHeader(http.StatusNoContent) // Discord returns 204
	}))
	defer server.Close()

	err := sendDiscord(context.Background(), server.URL, Notification{
		Status: StatusCritical, Previous: StatusWarning, Summary: "Things broke.",
		Findings: []Finding{{Severity: "critical", Resource: "pod a/b", Title: "CrashLoop", Detail: "restarting"}},
	})
	if err != nil {
		t.Fatalf("sendDiscord failed: %v", err)
	}

	embed := captured.Embeds[0]
	if !strings.Contains(embed.Title, "CRITICAL") || embed.Color != 0xE74C3C {
		t.Errorf("embed = %+v", embed)
	}
	if !strings.Contains(embed.Description, "Previous status: warning") {
		t.Errorf("description = %q", embed.Description)
	}
	if len(embed.Fields) != 1 || !strings.Contains(embed.Fields[0].Name, "CrashLoop") {
		t.Errorf("fields = %+v", embed.Fields)
	}

	// Non-2xx must surface as an error.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer failing.Close()
	if err := sendDiscord(context.Background(), failing.URL, Notification{Status: StatusWarning}); err == nil {
		t.Fatal("expected error for HTTP 400")
	}
}

func TestApplyValidatesNotificationSettings(t *testing.T) {
	rt := testRuntime(t)

	base := SettingsUpdate{Provider: "ollama", OllamaHost: "http://x", ReviewInterval: "5m", MonitorEnabled: true}

	// Enabling without any webhook must fail.
	bad := base
	bad.NotificationsEnabled = true
	if _, err := rt.Apply(bad); err == nil {
		t.Fatal("enabling notifications without a webhook should fail")
	}

	// Bad channel URLs must fail.
	bad = base
	bad.DiscordWebhook = "https://example.com/hook"
	if _, err := rt.Apply(bad); err == nil {
		t.Fatal("non-Discord webhook URL should fail")
	}
	bad = base
	bad.SlackWebhook = "https://example.com/hook"
	if _, err := rt.Apply(bad); err == nil {
		t.Fatal("non-Slack webhook URL should fail")
	}
	bad = base
	bad.TeamsWebhook = "http://insecure.example.com/hook"
	if _, err := rt.Apply(bad); err == nil {
		t.Fatal("non-https Teams webhook URL should fail")
	}

	// Slack alone is enough to enable; view masks all URLs.
	good := base
	good.NotificationsEnabled = true
	good.SlackWebhook = "https://hooks.slack.com/services/T0/B0/xyz"
	good.NotifyMinSeverity = "critical"
	view, err := rt.Apply(good)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if !view.NotificationsEnabled || !view.SlackWebhookSet || view.DiscordWebhookSet || view.NotifyMinSeverity != "critical" {
		t.Errorf("view = %+v", view)
	}

	// Adding Teams keeps Slack (write-only semantics per channel).
	next := base
	next.NotificationsEnabled = true
	next.TeamsWebhook = "https://prod-01.westeurope.logic.azure.com/workflows/abc"
	next.NotifyMinSeverity = "warning"
	view, err = rt.Apply(next)
	if err != nil {
		t.Fatalf("Apply with teams failed: %v", err)
	}
	if !view.SlackWebhookSet || !view.TeamsWebhookSet {
		t.Errorf("stored webhooks must be kept: %+v", view)
	}

	// The dispatcher now sees both channels.
	_, channels, _ := rt.NotificationSettings()
	if len(channels) != 2 {
		t.Errorf("channels = %+v, want slack+teams", channels)
	}
}
