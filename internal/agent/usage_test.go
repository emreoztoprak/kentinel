package agent

import (
	"path/filepath"
	"testing"
)

func TestPriceForLongestPrefixAndOllama(t *testing.T) {
	if _, ok := priceFor("ollama", "qwen3:0.6b"); ok {
		t.Error("ollama must be free (no pricing)")
	}
	if _, ok := priceFor("anthropic", "some-unknown-model"); ok {
		t.Error("unknown model must have no pricing")
	}
	p, ok := priceFor("anthropic", "claude-opus-4-8")
	if !ok || p.inputPerM != 5.00 || p.outputPerM != 25.00 {
		t.Errorf("claude-opus price = %+v ok=%v", p, ok)
	}
	// haiku must not match the broader "claude-" style prefixes incorrectly.
	if p, _ := priceFor("anthropic", "claude-haiku-4-5"); p.inputPerM != 1.00 {
		t.Errorf("claude-haiku price wrong: %+v", p)
	}
}

func TestUsageAggregationAndCost(t *testing.T) {
	s := NewPersistentStore(filepath.Join(t.TempDir(), "u.db"), 90, 20, discardLog())

	// 1M input + 1M output on claude-opus = $5 + $25 = $30.
	s.RecordUsage("anthropic", "claude-opus-4-8", "review",
		struct{ InputTokens, OutputTokens int }{1_000_000, 1_000_000})
	s.RecordUsage("anthropic", "claude-opus-4-8", "query",
		struct{ InputTokens, OutputTokens int }{500_000, 0})
	// Zero-token calls are ignored.
	s.RecordUsage("anthropic", "claude-opus-4-8", "query",
		struct{ InputTokens, OutputTokens int }{0, 0})

	sum, err := s.Usage(30, "anthropic", "claude-opus-4-8")
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if sum.InputTokens != 1_500_000 || sum.OutputTokens != 1_000_000 {
		t.Errorf("token totals wrong: in=%d out=%d", sum.InputTokens, sum.OutputTokens)
	}
	if !sum.HasPricing {
		t.Error("claude-opus should be priced")
	}
	// review $30 + query (0.5M input * $5) $2.50 = $32.50
	if got := round2(sum.CostUSD); got != 32.50 {
		t.Errorf("cost = %.2f, want 32.50", got)
	}
	if len(sum.BySource) != 2 {
		t.Fatalf("want review+query breakdown, got %d", len(sum.BySource))
	}
}

func TestUsageOllamaHasNoCost(t *testing.T) {
	s := NewPersistentStore(filepath.Join(t.TempDir(), "u.db"), 90, 20, discardLog())
	s.RecordUsage("ollama", "qwen3:0.6b", "review",
		struct{ InputTokens, OutputTokens int }{1000, 2000})
	sum, _ := s.Usage(30, "ollama", "qwen3:0.6b")
	if sum.HasPricing || sum.CostUSD != 0 {
		t.Errorf("ollama must have no cost: hasPricing=%v cost=%.2f", sum.HasPricing, sum.CostUSD)
	}
	if sum.InputTokens != 1000 || sum.OutputTokens != 2000 {
		t.Errorf("ollama tokens still counted: in=%d out=%d", sum.InputTokens, sum.OutputTokens)
	}
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
