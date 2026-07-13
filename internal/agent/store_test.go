package agent

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func sampleInsight(status Status, at time.Time) Insight {
	return Insight{
		Status: status, Summary: "summary " + string(status), CreatedAt: at,
		DurationMs: 42, Provider: "test", Model: "test-1",
		Findings: []Finding{{Severity: string(status), Resource: "pod a/b", Title: "t", Detail: "d"}},
	}
}

func TestPersistentStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "insights.db")
	store := NewPersistentStore(path, 90, 20, discardLog())
	if !store.Persistent() {
		t.Fatal("store should be persistent")
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	store.Add(sampleInsight(StatusHealthy, now.Add(-2*time.Minute)))
	store.Add(sampleInsight(StatusCritical, now))

	history, err := store.History(HistoryQuery{})
	if err != nil {
		t.Fatalf("History failed: %v", err)
	}
	if len(history) != 2 || history[0].Status != StatusCritical {
		t.Fatalf("history = %+v", history)
	}
	if len(history[0].Findings) != 1 || history[0].Findings[0].Resource != "pod a/b" {
		t.Errorf("findings not round-tripped: %+v", history[0].Findings)
	}
	store.Close()

	// Reopen the same file: history must survive and warm the cache.
	reopened := NewPersistentStore(path, 90, 20, discardLog())
	defer reopened.Close()
	if latest := reopened.Latest(); latest == nil || latest.Status != StatusCritical {
		t.Fatalf("Latest after reopen = %+v (restart must restore the cache)", latest)
	}
	history, err = reopened.History(HistoryQuery{})
	if err != nil || len(history) != 2 {
		t.Fatalf("history after reopen = %v, %v", history, err)
	}
}

func TestPersistentStoreFilters(t *testing.T) {
	store := NewPersistentStore(filepath.Join(t.TempDir(), "i.db"), 90, 20, discardLog())
	defer store.Close()

	base := time.Now().UTC().Add(-1 * time.Hour)
	for i := 0; i < 10; i++ {
		status := StatusHealthy
		if i%2 == 0 {
			status = StatusWarning
		}
		store.Add(sampleInsight(status, base.Add(time.Duration(i)*time.Minute)))
	}

	warnings, err := store.History(HistoryQuery{Status: StatusWarning})
	if err != nil || len(warnings) != 5 {
		t.Fatalf("status filter: %v, %v", warnings, err)
	}

	limited, err := store.History(HistoryQuery{Limit: 3})
	if err != nil || len(limited) != 3 {
		t.Fatalf("limit: %v, %v", limited, err)
	}

	windowed, err := store.History(HistoryQuery{Since: base.Add(7 * time.Minute)})
	if err != nil || len(windowed) != 3 {
		t.Fatalf("since filter: got %d, want 3 (%v)", len(windowed), err)
	}
}

func TestPersistentStoreRetention(t *testing.T) {
	store := NewPersistentStore(filepath.Join(t.TempDir(), "i.db"), 1, 20, discardLog()) // 1 day
	defer store.Close()

	store.Add(sampleInsight(StatusHealthy, time.Now().Add(-48*time.Hour))) // beyond retention
	store.Add(sampleInsight(StatusWarning, time.Now()))                    // triggers pruning

	history, err := store.History(HistoryQuery{})
	if err != nil {
		t.Fatalf("History failed: %v", err)
	}
	if len(history) != 1 || history[0].Status != StatusWarning {
		t.Fatalf("retention did not prune: %+v", history)
	}
}

func TestPersistentStoreTimeline(t *testing.T) {
	store := NewPersistentStore(filepath.Join(t.TempDir(), "i.db"), 90, 20, discardLog())
	defer store.Close()

	now := time.Now().UTC()
	store.Add(sampleInsight(StatusHealthy, now.Add(-30*time.Hour))) // outside 24h window
	store.Add(sampleInsight(StatusWarning, now.Add(-2*time.Hour)))
	store.Add(sampleInsight(StatusCritical, now.Add(-1*time.Hour)))

	points, err := store.Timeline(24 * time.Hour)
	if err != nil {
		t.Fatalf("Timeline failed: %v", err)
	}
	if len(points) != 2 || points[0].Status != StatusWarning || points[1].Status != StatusCritical {
		t.Fatalf("timeline = %+v (must be oldest-first, windowed)", points)
	}
}

func TestBrokenDBPathFallsBackToMemory(t *testing.T) {
	store := NewPersistentStore("/nonexistent-dir/insights.db", 90, 20, discardLog())
	defer store.Close()
	if store.Persistent() {
		t.Fatal("store with unopenable path must degrade to memory-only")
	}

	// ...and still work.
	store.Add(sampleInsight(StatusHealthy, time.Now()))
	if latest := store.Latest(); latest == nil {
		t.Fatal("memory fallback must still store insights")
	}
	if history, err := store.History(HistoryQuery{}); err != nil || len(history) != 1 {
		t.Fatalf("memory fallback history = %v, %v", history, err)
	}
}

func TestMemoryStoreHistoryFilters(t *testing.T) {
	store := NewStore(3)
	for i := 0; i < 5; i++ {
		store.Add(Insight{Summary: fmt.Sprintf("review %d", i), Status: StatusHealthy, CreatedAt: time.Now()})
	}
	history, err := store.History(HistoryQuery{})
	if err != nil {
		t.Fatalf("History failed: %v", err)
	}
	if len(history) != 3 || history[0].Summary != "review 4" {
		t.Fatalf("ring buffer semantics broken: %+v", history)
	}
	if store.Persistent() {
		t.Error("memory store must not report persistent")
	}
}
