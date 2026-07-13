package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/emreoztoprak/kentinel/internal/k8s"
	"github.com/emreoztoprak/kentinel/internal/llm"
)

const reviewSystemPrompt = `You are an experienced Kubernetes SRE reviewing the health of a cluster.
You receive a snapshot of the cluster state. Analyze it and respond with ONLY a JSON object (no prose, no code fences) in this exact shape:

{
  "status": "healthy" | "warning" | "critical",
  "summary": "one or two sentences describing overall cluster health",
  "findings": [
    {
      "severity": "info" | "warning" | "critical",
      "resource": "kind namespace/name of the affected resource",
      "title": "short problem title",
      "detail": "what is wrong and the likely cause",
      "recommendation": "concrete next step to investigate or fix"
    }
  ]
}

Rules:
- "critical": workloads are down or failing (crash loops, failed nodes, unavailable deployments).
- "warning": degraded but functioning (high restarts, pending pods, warning events that repeat).
- "healthy": nothing needs attention; findings may be empty.
- Completed Jobs with Succeeded pods are normal, not findings.
- If RESOURCE METRICS sections are present, use them: flag pods close to
  memory limits, CPU-throttled containers, and nodes under memory pressure —
  these are warnings even before anything crashes.
- Be specific: name the exact resources. Do not invent resources that are not in the snapshot.`

// Monitor runs the periodic cluster review loop. Interval, enablement and
// the LLM provider are read from the Runtime each iteration so settings
// changes apply without a restart.
type Monitor struct {
	k8s      *k8s.Client
	runtime  *Runtime
	store    *Store
	notifier *Dispatcher // may be nil (tests)
	log      *slog.Logger
}

// NewMonitor wires the review loop.
func NewMonitor(client *k8s.Client, runtime *Runtime, store *Store, notifier *Dispatcher, log *slog.Logger) *Monitor {
	return &Monitor{k8s: client, runtime: runtime, store: store, notifier: notifier, log: log}
}

// Run reviews immediately (when enabled), then on every tick until ctx is
// cancelled. A settings change wakes the loop for an immediate review with
// the new configuration. A failing review never stops the loop.
func (m *Monitor) Run(ctx context.Context) {
	enabled, interval := m.runtime.MonitorSettings()
	m.log.Info("monitor started", "interval", interval.String(), "enabled", enabled)

	for {
		enabled, interval = m.runtime.MonitorSettings()
		if enabled {
			m.reviewOnce(ctx)
		}

		select {
		case <-ctx.Done():
			m.log.Info("monitor stopped")
			return
		case <-time.After(interval):
		case <-m.runtime.Changed():
			m.log.Info("settings changed; re-reading monitor configuration")
		}
	}
}

func (m *Monitor) reviewOnce(ctx context.Context) {
	provider := m.runtime.Provider()
	start := time.Now()
	insight := m.review(ctx, provider)
	insight.CreatedAt = time.Now().UTC()
	insight.DurationMs = time.Since(start).Milliseconds()
	insight.Provider = provider.Name()
	insight.Model = provider.Model()
	m.store.Add(*insight)

	if m.notifier != nil {
		m.notifier.Process(ctx, *insight)
	}

	if insight.Status == StatusError {
		m.log.Error("cluster review failed", "error", insight.ReviewError, "duration", time.Since(start).Round(time.Millisecond))
		return
	}
	m.log.Info("cluster review completed",
		"status", insight.Status, "findings", len(insight.Findings),
		"duration", time.Since(start).Round(time.Millisecond))
}

func (m *Monitor) review(ctx context.Context, provider llm.Provider) *Insight {
	reviewCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	snap, err := snapshot(reviewCtx, m.k8s)
	if err != nil {
		return &Insight{Status: StatusError, Summary: "Could not collect cluster state.", ReviewError: err.Error()}
	}

	// Enrich with resource metrics when Prometheus is configured; a metrics
	// failure degrades to a note in the prompt, never fails the review.
	if prom := m.runtime.Prometheus(); prom != nil {
		snap += "\n" + metricsSnapshot(reviewCtx, prom)
	}

	resp, err := provider.Chat(reviewCtx, llm.ChatRequest{
		System:    reviewSystemPrompt,
		Messages:  []llm.Message{{Role: "user", Text: snap}},
		MaxTokens: 4096,
	})
	if err != nil {
		return &Insight{Status: StatusError, Summary: "The LLM review call failed.", ReviewError: err.Error()}
	}

	insight, err := parseInsight(resp.Text)
	if err != nil {
		return &Insight{Status: StatusError, Summary: "The LLM returned an unparseable review.", ReviewError: err.Error()}
	}
	return insight
}
