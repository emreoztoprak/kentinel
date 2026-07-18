package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: works in CGO_ENABLED=0 distroless images
)

// Store keeps insights. It always maintains an in-memory ring buffer (the
// hot cache for the dashboard) and, when a database path is configured,
// additionally persists every insight to SQLite so history survives pod
// restarts. If the database cannot be opened the store degrades to
// memory-only with a logged warning — persistence must never take the agent
// down.
//
// SQLite notes: WAL mode (readers don't block the single writer), one writer
// by design (the agent runs with replicas: 1), retention pruning on insert.
type Store struct {
	mu       sync.RWMutex
	insights []Insight // ring buffer, newest first
	capacity int

	db *sql.DB
	// retentionNanos is the pruning window in nanoseconds. Atomic because
	// pruning runs on the monitor goroutine (Add) while SetRetentionDays runs
	// on the settings-save goroutine.
	retentionNanos atomic.Int64
	settingsKey    []byte // AES-256 key protecting the settings table; nil disables settings persistence
	log            *slog.Logger
}

// HistoryQuery filters the history listing.
type HistoryQuery struct {
	Limit  int       // default 50, max 500
	Status Status    // empty = all
	Since  time.Time // zero = no lower bound
	Until  time.Time // zero = no upper bound
}

// TimelinePoint is one review in the compact trend view.
type TimelinePoint struct {
	Time   time.Time `json:"t"`
	Status Status    `json:"status"`
}

// NewStore creates a memory-only store keeping the last capacity insights.
func NewStore(capacity int) *Store {
	if capacity <= 0 {
		capacity = 20
	}
	return &Store{capacity: capacity, log: slog.Default()}
}

// NewPersistentStore creates a store backed by a SQLite file. On failure it
// returns a working memory-only store and logs the reason.
func NewPersistentStore(path string, retentionDays int, capacity int, log *slog.Logger) *Store {
	s := NewStore(capacity)
	s.log = log
	s.SetRetentionDays(retentionDays)

	db, err := openInsightDB(path)
	if err != nil {
		log.Error("opening insight database failed; history will not survive restarts",
			"path", path, "error", err)
		return s
	}
	s.db = db

	// Warm the ring buffer from disk so the dashboard has history
	// immediately after a restart.
	restored, err := s.queryInsights(HistoryQuery{Limit: capacity})
	if err != nil {
		log.Warn("loading persisted insights failed", "error", err)
	} else {
		s.insights = restored
		log.Info("insight history restored", "path", path, "restored", len(restored), "retentionDays", retentionDays)
	}

	key, err := loadOrCreateSettingsKey(path)
	if err != nil {
		log.Warn("settings encryption key unavailable; settings will not survive restarts", "error", err)
	} else {
		s.settingsKey = key
	}
	return s
}

// Persistent reports whether insights survive restarts.
func (s *Store) Persistent() bool { return s.db != nil }

// SetRetentionDays updates the pruning window, live. Non-positive values fall
// back to the 90-day default. Takes effect on the next insert.
func (s *Store) SetRetentionDays(days int) {
	if days <= 0 {
		days = 90
	}
	s.retentionNanos.Store(int64(time.Duration(days) * 24 * time.Hour))
}

// SettingsPersistent reports whether settings survive restarts (requires
// both a working database and a usable encryption key).
func (s *Store) SettingsPersistent() bool { return s.db != nil && s.settingsKey != nil }

// Close releases the database (tests; the agent runs until process exit).
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func openInsightDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Single writer by design; parallel connections only add lock contention.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",   // readers never block the writer
		"PRAGMA busy_timeout=5000;",  // wait instead of failing on transient locks
		"PRAGMA synchronous=NORMAL;", // safe with WAL, much faster than FULL
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("applying %s: %w", pragma, err)
		}
	}

	const schema = `
	CREATE TABLE IF NOT EXISTS insights (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL,
		status TEXT NOT NULL,
		summary TEXT NOT NULL,
		duration_ms INTEGER NOT NULL,
		provider TEXT NOT NULL,
		model TEXT NOT NULL,
		review_error TEXT NOT NULL DEFAULT '',
		findings TEXT NOT NULL DEFAULT '[]'
	);
	CREATE INDEX IF NOT EXISTS idx_insights_created_at ON insights(created_at);
	CREATE INDEX IF NOT EXISTS idx_insights_status ON insights(status, created_at);
	CREATE TABLE IF NOT EXISTS settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		updated_at TEXT NOT NULL,
		ciphertext BLOB NOT NULL
	);` + proposalsSchema + usageSchema
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}

	// The driver creates files 0644; tighten to owner-only like the
	// .settings.key sibling — the database holds the encrypted settings
	// blob and cluster insight history, and nothing else needs to read it.
	// After the schema write the -wal/-shm sidecars exist too.
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(f, 0o600); err != nil && !os.IsNotExist(err) {
			slog.Warn("tightening database file permissions failed", "file", f, "error", err)
		}
	}
	return db, nil
}

// Add inserts a new insight as the latest entry (cache + database).
func (s *Store) Add(insight Insight) {
	s.mu.Lock()
	s.insights = append([]Insight{insight}, s.insights...)
	if len(s.insights) > s.capacity {
		s.insights = s.insights[:s.capacity]
	}
	s.mu.Unlock()

	if s.db == nil {
		return
	}

	findings, err := json.Marshal(insight.Findings)
	if err != nil {
		findings = []byte("[]")
	}
	_, err = s.db.Exec(
		`INSERT INTO insights (created_at, status, summary, duration_ms, provider, model, review_error, findings)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		insight.CreatedAt.UTC().Format(time.RFC3339Nano),
		string(insight.Status), insight.Summary, insight.DurationMs,
		insight.Provider, insight.Model, insight.ReviewError, string(findings),
	)
	if err != nil {
		s.log.Error("persisting insight failed", "error", err)
		return
	}

	// Retention pruning piggybacks on the insert; indexed and cheap.
	cutoff := time.Now().Add(-time.Duration(s.retentionNanos.Load())).UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`DELETE FROM insights WHERE created_at < ?`, cutoff); err != nil {
		s.log.Warn("pruning old insights failed", "error", err)
	}
}

// Latest returns the most recent insight, or nil if none exists yet.
func (s *Store) Latest() *Insight {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.insights) == 0 {
		return nil
	}
	latest := s.insights[0]
	return &latest
}

// History returns insights newest-first, filtered by the query. Memory-only
// stores filter the ring buffer; persistent stores query the database.
func (s *Store) History(q HistoryQuery) ([]Insight, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 500 {
		q.Limit = 500
	}

	if s.db != nil {
		return s.queryInsights(q)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Insight, 0, q.Limit)
	for _, insight := range s.insights {
		if !matchesQuery(insight, q) {
			continue
		}
		out = append(out, insight)
		if len(out) == q.Limit {
			break
		}
	}
	return out, nil
}

// Timeline returns the (time, status) points of the last window, oldest
// first — the payload behind the dashboard trend strip.
func (s *Store) Timeline(window time.Duration) ([]TimelinePoint, error) {
	since := time.Now().Add(-window)

	if s.db != nil {
		rows, err := s.db.Query(
			`SELECT created_at, status FROM insights WHERE created_at >= ? ORDER BY created_at ASC`,
			since.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return nil, fmt.Errorf("querying timeline: %w", err)
		}
		defer rows.Close()
		var points []TimelinePoint
		for rows.Next() {
			var createdAt, status string
			if err := rows.Scan(&createdAt, &status); err != nil {
				return nil, err
			}
			t, _ := time.Parse(time.RFC3339Nano, createdAt)
			points = append(points, TimelinePoint{Time: t, Status: Status(status)})
		}
		return points, rows.Err()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	var points []TimelinePoint
	for i := len(s.insights) - 1; i >= 0; i-- { // ring is newest-first
		if s.insights[i].CreatedAt.After(since) {
			points = append(points, TimelinePoint{Time: s.insights[i].CreatedAt, Status: s.insights[i].Status})
		}
	}
	return points, nil
}

func (s *Store) queryInsights(q HistoryQuery) ([]Insight, error) {
	where := "1=1"
	args := []interface{}{}
	if q.Status != "" {
		where += " AND status = ?"
		args = append(args, string(q.Status))
	}
	if !q.Since.IsZero() {
		where += " AND created_at >= ?"
		args = append(args, q.Since.UTC().Format(time.RFC3339Nano))
	}
	if !q.Until.IsZero() {
		where += " AND created_at <= ?"
		args = append(args, q.Until.UTC().Format(time.RFC3339Nano))
	}
	args = append(args, q.Limit)

	rows, err := s.db.Query(
		`SELECT created_at, status, summary, duration_ms, provider, model, review_error, findings
		 FROM insights WHERE `+where+` ORDER BY created_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("querying insights: %w", err)
	}
	defer rows.Close()

	var out []Insight
	for rows.Next() {
		var insight Insight
		var createdAt, status, findings string
		if err := rows.Scan(&createdAt, &status, &insight.Summary, &insight.DurationMs,
			&insight.Provider, &insight.Model, &insight.ReviewError, &findings); err != nil {
			return nil, err
		}
		insight.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		insight.Status = Status(status)
		if err := json.Unmarshal([]byte(findings), &insight.Findings); err != nil {
			insight.Findings = nil
		}
		out = append(out, insight)
	}
	return out, rows.Err()
}

// persistedSettings is the on-disk (encrypted) form of Settings: JSON-
// friendly field types (ReviewInterval as a string) so it round-trips
// through SQLite cleanly.
type persistedSettings struct {
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	OllamaHost     string            `json:"ollamaHost"`
	APIKeys        map[string]string `json:"apiKeys"`
	ReviewInterval string            `json:"reviewInterval"`
	MonitorEnabled bool              `json:"monitorEnabled"`

	NotificationsEnabled bool   `json:"notificationsEnabled"`
	DiscordWebhook       string `json:"discordWebhook"`
	SlackWebhook         string `json:"slackWebhook"`
	TeamsWebhook         string `json:"teamsWebhook"`
	NotifyMinSeverity    string `json:"notifyMinSeverity"`

	PrometheusURL        string `json:"prometheusUrl"`
	InsightRetentionDays int    `json:"insightRetentionDays"`
}

func toPersistedSettings(s Settings) persistedSettings {
	return persistedSettings{
		Provider: s.Provider, Model: s.Model, OllamaHost: s.OllamaHost,
		APIKeys: cloneKeys(s.APIKeys), ReviewInterval: s.ReviewInterval.String(),
		MonitorEnabled: s.MonitorEnabled,

		NotificationsEnabled: s.NotificationsEnabled,
		DiscordWebhook:       s.DiscordWebhook,
		SlackWebhook:         s.SlackWebhook,
		TeamsWebhook:         s.TeamsWebhook,
		NotifyMinSeverity:    s.NotifyMinSeverity,

		PrometheusURL:        s.PrometheusURL,
		InsightRetentionDays: s.InsightRetentionDays,
	}
}

func (p persistedSettings) toSettings() (Settings, error) {
	interval, err := time.ParseDuration(p.ReviewInterval)
	if err != nil {
		return Settings{}, fmt.Errorf("persisted reviewInterval %q is invalid: %w", p.ReviewInterval, err)
	}
	return Settings{
		Provider: p.Provider, Model: p.Model, OllamaHost: p.OllamaHost,
		APIKeys: cloneKeys(p.APIKeys), ReviewInterval: interval, MonitorEnabled: p.MonitorEnabled,

		NotificationsEnabled: p.NotificationsEnabled,
		DiscordWebhook:       p.DiscordWebhook,
		SlackWebhook:         p.SlackWebhook,
		TeamsWebhook:         p.TeamsWebhook,
		NotifyMinSeverity:    p.NotifyMinSeverity,

		PrometheusURL:        p.PrometheusURL,
		InsightRetentionDays: p.InsightRetentionDays,
	}, nil
}

// SaveSettings persists settings as a single encrypted row, replacing
// whatever was there before. A no-op when persistence isn't available —
// consistent with Add(), a failure here must never break the caller.
func (s *Store) SaveSettings(settings Settings) {
	if !s.SettingsPersistent() {
		return
	}
	payload, err := json.Marshal(toPersistedSettings(settings))
	if err != nil {
		s.log.Error("encoding settings failed", "error", err)
		return
	}
	ciphertext, err := encryptSettings(s.settingsKey, payload)
	if err != nil {
		s.log.Error("encrypting settings failed", "error", err)
		return
	}
	_, err = s.db.Exec(
		`INSERT INTO settings (id, updated_at, ciphertext) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at, ciphertext = excluded.ciphertext`,
		time.Now().UTC().Format(time.RFC3339Nano), ciphertext,
	)
	if err != nil {
		s.log.Error("persisting settings failed", "error", err)
	}
}

// LoadSettings returns the persisted settings, or ok=false if none have
// been saved yet (or persistence isn't available).
func (s *Store) LoadSettings() (settings Settings, ok bool, err error) {
	if !s.SettingsPersistent() {
		return Settings{}, false, nil
	}
	var ciphertext []byte
	err = s.db.QueryRow(`SELECT ciphertext FROM settings WHERE id = 1`).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, fmt.Errorf("querying settings: %w", err)
	}
	plaintext, err := decryptSettings(s.settingsKey, ciphertext)
	if err != nil {
		return Settings{}, false, fmt.Errorf("decrypting settings: %w", err)
	}
	var persisted persistedSettings
	if err := json.Unmarshal(plaintext, &persisted); err != nil {
		return Settings{}, false, fmt.Errorf("decoding settings: %w", err)
	}
	settings, err = persisted.toSettings()
	if err != nil {
		return Settings{}, false, err
	}
	return settings, true, nil
}

func matchesQuery(insight Insight, q HistoryQuery) bool {
	if q.Status != "" && insight.Status != q.Status {
		return false
	}
	if !q.Since.IsZero() && insight.CreatedAt.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && insight.CreatedAt.After(q.Until) {
		return false
	}
	return true
}
