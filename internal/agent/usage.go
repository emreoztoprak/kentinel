package agent

import (
	"fmt"
	"strings"
	"time"
)

// usageSchema is folded into the store schema (see openInsightDB). One row per
// LLM call, so we can aggregate by time window and source.
const usageSchema = `
	CREATE TABLE IF NOT EXISTS token_usage (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL,
		provider TEXT NOT NULL,
		model TEXT NOT NULL,
		source TEXT NOT NULL,           -- "review" | "query"
		input_tokens INTEGER NOT NULL,
		output_tokens INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_usage_created_at ON token_usage(created_at);`

// modelPrice is USD per 1,000,000 tokens. Prices are approximate and change
// over time — the UI labels the resulting cost an estimate. Ollama and any
// unlisted model have no price (tokens are still counted).
type modelPrice struct {
	inputPerM  float64
	outputPerM float64
}

// prices maps a model-ID prefix to its price. Longest-prefix match wins, so
// "claude-opus-4-8" matches before a hypothetical "claude-opus". Keep in sync
// with the providers' public pricing; approximate is fine.
var prices = map[string]modelPrice{
	"claude-opus":    {5.00, 25.00},
	"claude-sonnet":  {3.00, 15.00},
	"claude-haiku":   {1.00, 5.00},
	"gpt-5":          {1.25, 10.00},
	"gpt-4.1":        {2.00, 8.00},
	"gpt-4o-mini":    {0.15, 0.60},
	"gpt-4o":         {2.50, 10.00},
	"deepseek-chat":  {0.27, 1.10},
	"deepseek-reaso": {0.55, 2.19},
	"gemini-2.5-pro": {1.25, 10.00},
	"gemini-2.5-fla": {0.30, 2.50},
	"gemini":         {0.30, 2.50},
}

// priceFor returns the price for a model and whether one is known. Ollama
// (local) is always free.
func priceFor(provider, model string) (modelPrice, bool) {
	if provider == "ollama" {
		return modelPrice{}, false
	}
	var best string
	for prefix := range prices {
		if strings.HasPrefix(model, prefix) && len(prefix) > len(best) {
			best = prefix
		}
	}
	if best == "" {
		return modelPrice{}, false
	}
	return prices[best], true
}

// RecordUsage stores one call's token usage. No-op when persistence is off or
// there are no tokens (some providers omit usage). Never errors to the caller —
// cost tracking must not disrupt a review or query.
func (s *Store) RecordUsage(provider, model, source string, u struct{ InputTokens, OutputTokens int }) {
	if s.db == nil || (u.InputTokens == 0 && u.OutputTokens == 0) {
		return
	}
	_, err := s.db.Exec(
		`INSERT INTO token_usage (created_at, provider, model, source, input_tokens, output_tokens)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano), provider, model, source, u.InputTokens, u.OutputTokens)
	if err != nil {
		s.log.Warn("recording token usage failed", "error", err)
		return
	}
	// Bound growth with the same retention window as insights.
	cutoff := time.Now().Add(-time.Duration(s.retentionNanos.Load())).UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`DELETE FROM token_usage WHERE created_at < ?`, cutoff); err != nil {
		s.log.Warn("pruning token usage failed", "error", err)
	}
}

// SourceUsage is the token/cost aggregate for one source (review or query).
type SourceUsage struct {
	Source       string  `json:"source"`
	Calls        int     `json:"calls"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CostUSD      float64 `json:"costUsd"`
}

// UsageSummary aggregates token usage (and estimated cost) over the last
// `days`. hasPricing is false when the active model has no known price (e.g.
// Ollama), in which case CostUSD figures are zero and the UI shows tokens only.
type UsageSummary struct {
	Days         int           `json:"days"`
	Provider     string        `json:"provider"`
	Model        string        `json:"model"`
	HasPricing   bool          `json:"hasPricing"`
	InputTokens  int64         `json:"inputTokens"`
	OutputTokens int64         `json:"outputTokens"`
	CostUSD      float64       `json:"costUsd"`
	BySource     []SourceUsage `json:"bySource"`
}

// Usage aggregates token usage over the last `days`, pricing each row by its
// own recorded model (so a period spanning a provider switch is priced
// correctly). provider/model describe the CURRENTLY active config, for the
// hasPricing hint and display.
func (s *Store) Usage(days int, provider, model string) (UsageSummary, error) {
	sum := UsageSummary{Days: days, Provider: provider, Model: model}
	if _, ok := priceFor(provider, model); ok {
		sum.HasPricing = true
	}
	if s.db == nil {
		return sum, nil
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	rows, err := s.db.Query(
		`SELECT source, provider, model, COUNT(*), SUM(input_tokens), SUM(output_tokens)
		 FROM token_usage WHERE created_at >= ? GROUP BY source, provider, model`, since)
	if err != nil {
		return sum, fmt.Errorf("querying usage: %w", err)
	}
	defer rows.Close()

	bySource := map[string]*SourceUsage{}
	for rows.Next() {
		var source, rowProvider, rowModel string
		var calls int
		var in, out int64
		if err := rows.Scan(&source, &rowProvider, &rowModel, &calls, &in, &out); err != nil {
			return sum, err
		}
		// Price each row by ITS OWN provider/model, so a window spanning a
		// provider switch is costed correctly.
		cost := 0.0
		if p, ok := priceFor(rowProvider, rowModel); ok {
			cost = float64(in)/1e6*p.inputPerM + float64(out)/1e6*p.outputPerM
		}
		agg := bySource[source]
		if agg == nil {
			agg = &SourceUsage{Source: source}
			bySource[source] = agg
		}
		agg.Calls += calls
		agg.InputTokens += in
		agg.OutputTokens += out
		agg.CostUSD += cost

		sum.InputTokens += in
		sum.OutputTokens += out
		sum.CostUSD += cost
	}
	if err := rows.Err(); err != nil {
		return sum, err
	}
	for _, src := range []string{"review", "query"} {
		if agg := bySource[src]; agg != nil {
			sum.BySource = append(sum.BySource, *agg)
		}
	}
	return sum, nil
}
