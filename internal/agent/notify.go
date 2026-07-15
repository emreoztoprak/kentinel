package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Notification is one alert about a cluster status change.
type Notification struct {
	Status   Status
	Previous Status // empty on the first review
	Summary  string
	Findings []Finding
	Test     bool // true for "Send test notification"
}

// Dispatcher decides when a review result warrants a notification and sends
// it to every configured channel (Discord, Slack, Teams). The rule: notify
// on status *transitions* (healthy→warning→critical and recoveries), never
// on every review — dedup is the feature. Reviews with status "error"
// (LLM/cluster failures) are ignored entirely; they are visible in the UI
// and shouldn't page anyone.
type Dispatcher struct {
	runtime *Runtime
	log     *slog.Logger
	// senders maps channel name -> send function (replaced in tests).
	senders map[string]func(ctx context.Context, webhook string, n Notification) error

	mu   sync.Mutex
	last Status // last non-error status seen (tracked even while disabled)
}

// NewDispatcher wires the notification pipeline with all channel senders.
func NewDispatcher(runtime *Runtime, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		runtime: runtime,
		log:     log,
		senders: map[string]func(context.Context, string, Notification) error{
			"discord": sendDiscord,
			"slack":   sendSlack,
			"teams":   sendTeams,
		},
	}
}

// deliver sends a notification to all configured channels, logging (not
// propagating) per-channel failures. Returns the first error for callers
// that surface it (the test endpoint).
func (d *Dispatcher) deliver(ctx context.Context, channels []NotificationChannel, n Notification) error {
	var firstErr error
	for _, channel := range channels {
		send, ok := d.senders[channel.Name]
		if !ok {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := send(sendCtx, channel.URL, n)
		cancel()
		if err != nil {
			d.log.Error("sending notification failed", "channel", channel.Name, "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", channel.Name, err)
			}
			continue
		}
		d.log.Info("notification sent", "channel", channel.Name, "status", n.Status, "previous", string(n.Previous))
	}
	return firstErr
}

var severityRank = map[Status]int{StatusHealthy: 0, StatusWarning: 1, StatusCritical: 2}

// Process inspects a completed review and notifies if the status crossed a
// transition boundary. Failures are logged, never propagated — a broken
// webhook must not affect the review loop.
func (d *Dispatcher) Process(ctx context.Context, insight Insight) {
	if insight.Status == StatusError {
		return
	}

	d.mu.Lock()
	previous := d.last
	d.last = insight.Status
	d.mu.Unlock()

	if previous == insight.Status {
		return // no transition, no noise
	}

	enabled, channels, minSeverity := d.runtime.NotificationSettings()
	if !enabled || len(channels) == 0 {
		return
	}

	// Notify when either side of the transition reaches the threshold:
	// healthy→warning (with min=warning), warning→critical, and recoveries
	// like critical→healthy (the previous side qualifies).
	threshold := severityRank[Status(minSeverity)]
	newQualifies := severityRank[insight.Status] >= threshold
	prevQualifies := previous != "" && severityRank[previous] >= threshold
	if !newQualifies && !prevQualifies {
		return
	}

	_ = d.deliver(ctx, channels, Notification{
		Status:   insight.Status,
		Previous: previous,
		Summary:  insight.Summary,
		Findings: insight.Findings,
	})
}

// SendTest delivers a test notification to every configured channel.
func (d *Dispatcher) SendTest(ctx context.Context) error {
	_, channels, _ := d.runtime.NotificationSettings()
	if len(channels) == 0 {
		return fmt.Errorf("no webhook URL is configured (Discord, Slack, or Teams)")
	}
	return d.deliver(ctx, channels, Notification{
		Status:  StatusWarning,
		Summary: "This is a test notification from Kentinel. Your webhook works!",
		Test:    true,
		Findings: []Finding{{
			Severity: "warning",
			Resource: "example pod demo/example-123",
			Title:    "Example finding",
			Detail:   "Real alerts fire when the cluster status changes (e.g. healthy → warning).",
		}},
	})
}

// isDiscordWebhook loosely validates the URL shape before storing it.
func isDiscordWebhook(url string) bool {
	return strings.HasPrefix(url, "https://discord.com/api/webhooks/") ||
		strings.HasPrefix(url, "https://discordapp.com/api/webhooks/")
}

// --- Discord wire format ---

type discordPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color"`
	Fields      []discordField `json:"fields,omitempty"`
	Timestamp   string         `json:"timestamp"`
	Footer      *discordFooter `json:"footer,omitempty"`
}

type discordField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type discordFooter struct {
	Text string `json:"text"`
}

// sendDiscord posts one embed to a Discord webhook.
func sendDiscord(ctx context.Context, webhook string, n Notification) error {
	title, color := embedStyle(n)

	description := n.Summary
	if n.Previous != "" {
		description += fmt.Sprintf("\n\n_Previous status: %s_", n.Previous)
	}

	embed := discordEmbed{
		Title:       title,
		Description: truncateRunes(description, 2000),
		Color:       color,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Footer:      &discordFooter{Text: "Kentinel"},
	}
	for i, f := range n.Findings {
		if i == 5 {
			embed.Fields = append(embed.Fields, discordField{
				Name: "…", Value: fmt.Sprintf("and %d more findings", len(n.Findings)-5),
			})
			break
		}
		value := f.Resource
		if f.Detail != "" {
			value += " — " + f.Detail
		}
		embed.Fields = append(embed.Fields, discordField{
			Name:  fmt.Sprintf("[%s] %s", strings.ToUpper(f.Severity), truncateRunes(f.Title, 200)),
			Value: truncateRunes(value, 1000),
		})
	}

	payload, err := json.Marshal(discordPayload{Embeds: []discordEmbed{embed}})
	if err != nil {
		return fmt.Errorf("encoding discord payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("discord webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("discord webhook returned HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func embedStyle(n Notification) (title string, color int) {
	prefix := ""
	if n.Test {
		prefix = "[TEST] "
	}
	switch n.Status {
	case StatusCritical:
		return prefix + "⛔ Cluster status: CRITICAL", 0xE74C3C
	case StatusWarning:
		return prefix + "⚠️ Cluster status: WARNING", 0xF1C40F
	default:
		if n.Previous != "" {
			return prefix + "✅ Cluster recovered: HEALTHY", 0x2ECC71
		}
		return prefix + "✅ Cluster status: HEALTHY", 0x2ECC71
	}
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// --- Slack wire format (incoming webhooks, attachments for the color bar) ---

type slackPayload struct {
	Attachments []slackAttachment `json:"attachments"`
}

type slackAttachment struct {
	Color  string       `json:"color"`
	Title  string       `json:"title"`
	Text   string       `json:"text"`
	Fields []slackField `json:"fields,omitempty"`
	Footer string       `json:"footer"`
	Ts     int64        `json:"ts"`
}

type slackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

// sendSlack posts one colored attachment to a Slack incoming webhook.
func sendSlack(ctx context.Context, webhook string, n Notification) error {
	title, color := embedStyle(n)

	text := n.Summary
	if n.Previous != "" {
		text += fmt.Sprintf("\n_Previous status: %s_", n.Previous)
	}

	attachment := slackAttachment{
		Color:  fmt.Sprintf("#%06X", color),
		Title:  title,
		Text:   truncateRunes(text, 2000),
		Footer: "Kentinel",
		Ts:     time.Now().Unix(),
	}
	for i, f := range n.Findings {
		if i == 5 {
			attachment.Fields = append(attachment.Fields, slackField{
				Title: "…", Value: fmt.Sprintf("and %d more findings", len(n.Findings)-5),
			})
			break
		}
		value := f.Resource
		if f.Detail != "" {
			value += " — " + f.Detail
		}
		attachment.Fields = append(attachment.Fields, slackField{
			Title: fmt.Sprintf("[%s] %s", strings.ToUpper(f.Severity), truncateRunes(f.Title, 200)),
			Value: truncateRunes(value, 1000),
		})
	}

	return postJSON(ctx, "slack", webhook, slackPayload{Attachments: []slackAttachment{attachment}})
}

// --- Teams wire format (Adaptive Card — works with Power Automate
// "Workflows" webhooks, the go-forward Teams integration) ---

func sendTeams(ctx context.Context, webhook string, n Notification) error {
	title, _ := embedStyle(n)
	titleColor := map[Status]string{
		StatusCritical: "attention",
		StatusWarning:  "warning",
	}[n.Status]
	if titleColor == "" {
		titleColor = "good"
	}

	body := []map[string]interface{}{
		{"type": "TextBlock", "text": title, "weight": "bolder", "size": "medium", "color": titleColor, "wrap": true},
		{"type": "TextBlock", "text": truncateRunes(n.Summary, 2000), "wrap": true},
	}
	if n.Previous != "" {
		body = append(body, map[string]interface{}{
			"type": "TextBlock", "text": "Previous status: " + string(n.Previous), "isSubtle": true, "wrap": true,
		})
	}
	for i, f := range n.Findings {
		if i == 5 {
			body = append(body, map[string]interface{}{
				"type": "TextBlock", "text": fmt.Sprintf("… and %d more findings", len(n.Findings)-5), "isSubtle": true,
			})
			break
		}
		value := f.Resource
		if f.Detail != "" {
			value += " — " + f.Detail
		}
		body = append(body,
			map[string]interface{}{
				"type": "TextBlock", "wrap": true, "weight": "bolder",
				"text": fmt.Sprintf("[%s] %s", strings.ToUpper(f.Severity), truncateRunes(f.Title, 200)),
			},
			map[string]interface{}{"type": "TextBlock", "wrap": true, "text": truncateRunes(value, 1000)},
		)
	}

	payload := map[string]interface{}{
		"type": "message",
		"attachments": []map[string]interface{}{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]interface{}{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard",
				"version": "1.4",
				"body":    body,
			},
		}},
	}
	return postJSON(ctx, "teams", webhook, payload)
}

// postJSON posts a payload to a webhook and errors on non-2xx.
func postJSON(ctx context.Context, channel, webhook string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding %s payload: %w", channel, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%s: %w", channel, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s webhook request failed: %w", channel, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("%s webhook returned HTTP %d: %s", channel, resp.StatusCode, string(body))
	}
	return nil
}
