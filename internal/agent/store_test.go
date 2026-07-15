package agent

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
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

func sampleSettings() Settings {
	return Settings{
		Provider:       "anthropic",
		Model:          "claude-opus-4-8",
		OllamaHost:     "http://ollama:11434",
		APIKeys:        map[string]string{"anthropic": "sk-ant-super-secret"},
		ReviewInterval: 10 * time.Minute,
		MonitorEnabled: true,
		SlackWebhook:   "https://hooks.slack.com/services/T/B/secret-path",
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	store := NewPersistentStore(path, 90, 20, discardLog())
	if !store.SettingsPersistent() {
		t.Fatal("settings should be persistent")
	}

	if _, ok, err := store.LoadSettings(); err != nil || ok {
		t.Fatalf("LoadSettings on empty store: ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	want := sampleSettings()
	store.SaveSettings(want)

	got, ok, err := store.LoadSettings()
	if err != nil || !ok {
		t.Fatalf("LoadSettings failed: ok=%v err=%v", ok, err)
	}
	if got.Provider != want.Provider || got.Model != want.Model ||
		got.ReviewInterval != want.ReviewInterval || got.APIKeys["anthropic"] != want.APIKeys["anthropic"] ||
		got.SlackWebhook != want.SlackWebhook {
		t.Fatalf("round-tripped settings = %+v, want %+v", got, want)
	}

	// Saving again must replace, not append (single row, upsert).
	want.Model = "claude-sonnet-5"
	store.SaveSettings(want)
	got, _, err = store.LoadSettings()
	if err != nil || got.Model != "claude-sonnet-5" {
		t.Fatalf("second save did not replace: got=%+v err=%v", got, err)
	}
}

// TestSettingsAreNotStoredAsPlaintext is the concrete regression test for
// "tokens/secrets must not be plaintext at rest": it reads the raw SQLite
// file bytes directly (bypassing the driver, the same way an attacker with
// PVC file access would) and confirms the API key and webhook URL never
// appear verbatim.
func TestSettingsAreNotStoredAsPlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	store := NewPersistentStore(path, 90, 20, discardLog())
	if !store.SettingsPersistent() {
		t.Fatal("settings should be persistent")
	}
	secret := sampleSettings()
	store.SaveSettings(secret)

	// Force a WAL checkpoint so the data is actually in the main file, not
	// just the -wal sidecar, before reading raw bytes back.
	if _, err := store.db.Exec("PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
		t.Fatalf("checkpoint failed: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading raw db file: %v", err)
	}
	for _, needle := range []string{secret.APIKeys["anthropic"], secret.SlackWebhook} {
		if bytes.Contains(raw, []byte(needle)) {
			t.Errorf("raw database file contains plaintext secret %q", needle)
		}
	}

	// The key file itself must exist, be restrictive, and be a sibling of
	// the database (same volume, no extra mount needed).
	keyPath := filepath.Join(filepath.Dir(path), ".settings.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("settings key file missing: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("settings key file mode = %o, want 0600", perm)
	}
}

// TestSettingsKeyReusedAcrossRestarts confirms a second Store pointed at
// the same file can decrypt what the first one wrote — the key must be
// loaded, not regenerated, on every boot.
func TestSettingsKeyReusedAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	first := NewPersistentStore(path, 90, 20, discardLog())
	first.SaveSettings(sampleSettings())
	if err := first.Close(); err != nil {
		t.Fatalf("closing first store: %v", err)
	}

	second := NewPersistentStore(path, 90, 20, discardLog())
	if !second.SettingsPersistent() {
		t.Fatal("second store should also report settings persistent")
	}
	got, ok, err := second.LoadSettings()
	if err != nil || !ok {
		t.Fatalf("second store could not decrypt settings written by the first: ok=%v err=%v", ok, err)
	}
	if got.APIKeys["anthropic"] != "sk-ant-super-secret" {
		t.Fatalf("decrypted settings wrong after reopening: %+v", got)
	}
}

// TestSettingsPersistentFalseWithoutKey confirms SaveSettings/LoadSettings
// degrade gracefully (no panic, no error surfaced to the caller) when the
// encryption key isn't available — mirrors how memory-only Store already
// behaves for insights.
func TestSettingsPersistentFalseWithoutKey(t *testing.T) {
	s := NewStore(20) // memory-only: no db, no key
	if s.SettingsPersistent() {
		t.Fatal("memory-only store must not report settings persistent")
	}
	s.SaveSettings(sampleSettings()) // must not panic
	if _, ok, err := s.LoadSettings(); err != nil || ok {
		t.Fatalf("LoadSettings on memory-only store: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}
