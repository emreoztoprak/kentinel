package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// reportWindow is how far back the daily report looks.
const reportWindow = 24 * time.Hour

// Reporter sends a once-a-day digest of the last 24 hours — reviews,
// incidents, remediation proposals, and LLM usage — to the notification
// webhooks. It is pure reporting: everything it says comes from data the
// agent already stored, no extra LLM calls and no cluster access.
type Reporter struct {
	store    *Store
	runtime  *Runtime
	notifier *Dispatcher
	log      *slog.Logger

	mu       sync.Mutex
	lastSent string // "2006-01-02" of the last delivered scheduled report
}

// NewReporter wires the daily-report loop.
func NewReporter(store *Store, runtime *Runtime, notifier *Dispatcher, log *slog.Logger) *Reporter {
	return &Reporter{store: store, runtime: runtime, notifier: notifier, log: log}
}

// Run checks once a minute whether the configured send time (UTC) has been
// reached. A minute tick (rather than a long sleep until the target time)
// means settings changes apply without any wake-up plumbing, at negligible
// cost. At most one scheduled report goes out per UTC day.
func (rp *Reporter) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			enabled, hour, minute := rp.runtime.ReportSettings()
			nowUTC := now.UTC()
			if !enabled || nowUTC.Hour() != hour || nowUTC.Minute() != minute {
				continue
			}
			day := nowUTC.Format("2006-01-02")
			rp.mu.Lock()
			alreadySent := rp.lastSent == day
			if !alreadySent {
				rp.lastSent = day
			}
			rp.mu.Unlock()
			if alreadySent {
				continue
			}
			if err := rp.Send(ctx); err != nil {
				rp.log.Error("sending daily report failed", "error", err)
			}
		}
	}
}

// Send builds the digest and delivers it to every configured webhook. Also
// called directly by the "Send report now" API endpoint.
func (rp *Reporter) Send(ctx context.Context) error {
	_, channels, _ := rp.runtime.NotificationSettings()
	if len(channels) == 0 {
		return fmt.Errorf("no webhook URL is configured (Discord, Slack, or Teams)")
	}
	status, headline, sections := rp.build()
	if err := rp.notifier.deliver(ctx, channels, Notification{
		Status:   status,
		Summary:  headline,
		Sections: sections,
		Report:   true,
	}); err != nil {
		return err
	}
	rp.log.Info("daily report sent", "channels", len(channels))
	return nil
}

var statusEmoji = map[Status]string{
	StatusHealthy:  "✅",
	StatusWarning:  "⚠️",
	StatusCritical: "⛔",
}

var proposalEmoji = map[ProposalStatus]string{
	ProposalApplied:  "✅",
	ProposalRejected: "🚫",
	ProposalPending:  "⏳",
	ProposalFailed:   "❌",
}

// build composes the report as a one-line headline plus titled sections.
// Formatting stays plain text + emoji: each channel renders the sections
// natively (Slack fields, Discord embed fields, Teams text blocks), so no
// markdown is used anywhere. The overall status (title and color) is the
// latest review's status.
func (rp *Reporter) build() (Status, string, []ReportSection) {
	now := time.Now().UTC()
	since := now.Add(-reportWindow)

	// Reviews: counts and incidents from the timeline (all points, not
	// capped like History).
	points, err := rp.store.Timeline(reportWindow)
	if err != nil {
		rp.log.Warn("report: querying timeline failed", "error", err)
	}
	counts := map[Status]int{}
	incidents := 0
	wasHealthy := true
	for _, p := range points {
		counts[p.Status]++
		bad := p.Status == StatusWarning || p.Status == StatusCritical
		if bad && wasHealthy {
			incidents++
		}
		if p.Status != StatusError {
			wasHealthy = !bad
		}
	}

	status := StatusHealthy
	latest := rp.store.Latest()
	if latest != nil {
		status = latest.Status
		if status == StatusError {
			status = StatusWarning
		}
	}

	var reviews []string
	if len(points) == 0 {
		reviews = append(reviews, "None ran — periodic review is off or the agent just started.")
	} else {
		line := fmt.Sprintf("%d reviews: %d ✅ healthy · %d ⚠️ warning · %d ⛔ critical",
			len(points), counts[StatusHealthy], counts[StatusWarning], counts[StatusCritical])
		if counts[StatusError] > 0 {
			line += fmt.Sprintf(" · %d failed to run", counts[StatusError])
		}
		reviews = append(reviews, line)
		if incidents == 0 {
			reviews = append(reviews, "Never dropped below healthy 🎉")
		} else {
			reviews = append(reviews, fmt.Sprintf("Dropped below healthy %d time(s).", incidents))
		}
	}
	if latest != nil && latest.Summary != "" {
		// Label a review that predates the window as such — otherwise
		// "no reviews ran" and a current-sounding verdict contradict
		// each other.
		label := "Current"
		if age := now.Sub(latest.CreatedAt); age > reportWindow {
			label = fmt.Sprintf("Last review, %s ago", humanDuration(age))
		}
		reviews = append(reviews, fmt.Sprintf("%s: %s %s — %s",
			label, statusEmoji[status], strings.ToUpper(string(latest.Status)), latest.Summary))
	}
	sections := []ReportSection{{Title: "Cluster reviews (24h)", Lines: reviews}}

	// Changes: remediation proposals created or decided in the window.
	// Every line here had (or awaits) an explicit human decision — the
	// report is the record, not the approver.
	proposals, err := rp.store.ProposalsSince(since)
	if err != nil {
		rp.log.Warn("report: querying proposals failed", "error", err)
	}
	var changes []string
	if len(proposals) == 0 {
		changes = append(changes, "No remediation proposals were made or decided.")
	} else {
		for i, p := range proposals {
			if i == 8 {
				changes = append(changes, fmt.Sprintf("… and %d more — see the dashboard.", len(proposals)-8))
				break
			}
			changes = append(changes, fmt.Sprintf("%s %s · %s %s/%s — %s",
				proposalEmoji[p.Status], p.Status, singularKind(p.Kind), p.Namespace, p.Name,
				truncateRunes(firstLine(p.Rationale), 110)))
		}
	}
	sections = append(sections, ReportSection{Title: "Changes (human-approved)", Lines: changes})

	// LLM usage over the same window.
	view := rp.runtime.View()
	usage, err := rp.store.Usage(1, view.Provider, view.Model)
	if err != nil {
		rp.log.Warn("report: querying usage failed", "error", err)
	} else if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		calls := 0
		for _, src := range usage.BySource {
			calls += src.Calls
		}
		line := fmt.Sprintf("%d calls · %s in / %s out tokens",
			calls, formatTokens(usage.InputTokens), formatTokens(usage.OutputTokens))
		if usage.CostUSD > 0 {
			line += fmt.Sprintf(" · ≈ $%.2f estimated", usage.CostUSD)
		} else {
			line += " · free (local model)"
		}
		sections = append(sections, ReportSection{Title: "LLM usage", Lines: []string{line}})
	}

	// One-line headline shown above the sections.
	headline := fmt.Sprintf("Last 24h: %d reviews · %d incident(s) · %d change(s).",
		len(points), incidents, len(proposals))
	if len(points) == 0 {
		headline = fmt.Sprintf("Last 24h: no reviews ran · %d change(s).", len(proposals))
	}
	return status, headline, sections
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// singularKind renders the resource-browser kind ("deployments") as a
// readable singular ("deployment"). Crude trim; fine for display.
func singularKind(kind string) string {
	return strings.TrimSuffix(kind, "s")
}

// humanDuration renders an age compactly: "3h", "2d".
func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

// formatTokens renders token counts compactly (1234567 -> "1.2M").
func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}
