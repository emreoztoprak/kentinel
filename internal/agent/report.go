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
	status, text := rp.build()
	if err := rp.notifier.deliver(ctx, channels, Notification{
		Status:  status,
		Summary: text,
		Report:  true,
	}); err != nil {
		return err
	}
	rp.log.Info("daily report sent", "channels", len(channels))
	return nil
}

// build composes the report text from stored data. The overall status (used
// for the title and color) is the latest review's status.
func (rp *Reporter) build() (Status, string) {
	now := time.Now().UTC()
	since := now.Add(-reportWindow)
	var b strings.Builder

	// Reviews: counts and incidents from the timeline (all points, not
	// capped like History).
	status := StatusHealthy
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

	latest := rp.store.Latest()
	if latest != nil {
		status = latest.Status
		if status == StatusError {
			status = StatusWarning
		}
	}

	if len(points) == 0 {
		b.WriteString("No cluster reviews ran in the last 24h — periodic review may be disabled.\n")
	} else {
		b.WriteString(fmt.Sprintf("**Reviews** — %d in the last 24h: %d healthy, %d warning, %d critical",
			len(points), counts[StatusHealthy], counts[StatusWarning], counts[StatusCritical]))
		if counts[StatusError] > 0 {
			b.WriteString(fmt.Sprintf(" (%d failed to run)", counts[StatusError]))
		}
		b.WriteString("\n")
		if incidents == 0 {
			b.WriteString("**Incidents** — none: the cluster never dropped below healthy.\n")
		} else {
			b.WriteString(fmt.Sprintf("**Incidents** — %d time(s) the status dropped below healthy.\n", incidents))
		}
	}
	if latest != nil && latest.Summary != "" {
		b.WriteString(fmt.Sprintf("**Now** — %s: %s\n", strings.ToUpper(string(latest.Status)), latest.Summary))
	}

	// Changes: remediation proposals created or decided in the window.
	// Every line here had (or awaits) an explicit human decision — the
	// report is the record, not the approver.
	proposals, err := rp.store.ProposalsSince(since)
	if err != nil {
		rp.log.Warn("report: querying proposals failed", "error", err)
	}
	b.WriteString("\n")
	if len(proposals) == 0 {
		b.WriteString("**Changes** — no remediation proposals were made or decided.\n")
	} else {
		b.WriteString(fmt.Sprintf("**Changes** — %d remediation proposal(s):\n", len(proposals)))
		for i, p := range proposals {
			if i == 8 {
				b.WriteString(fmt.Sprintf("• … and %d more (see the dashboard)\n", len(proposals)-8))
				break
			}
			b.WriteString(fmt.Sprintf("• %s — %s %s/%s: %s\n",
				p.Status, p.Kind, p.Namespace, p.Name, truncateRunes(firstLine(p.Rationale), 120)))
		}
	}

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
		b.WriteString(fmt.Sprintf("\n**LLM usage** — %d calls, %s in / %s out tokens",
			calls, formatTokens(usage.InputTokens), formatTokens(usage.OutputTokens)))
		if usage.CostUSD > 0 {
			b.WriteString(fmt.Sprintf(" (≈ $%.2f estimated)", usage.CostUSD))
		}
		b.WriteString("\n")
	}

	return status, strings.TrimRight(b.String(), "\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
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
