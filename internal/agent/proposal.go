package agent

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ProposalStatus is the lifecycle of a remediation proposal. The status
// transitions plus their timestamps ARE the audit trail: what was proposed,
// when it was decided, and the outcome.
type ProposalStatus string

const (
	ProposalPending  ProposalStatus = "pending"  // awaiting a human decision
	ProposalRejected ProposalStatus = "rejected" // a human declined it
	ProposalApplied  ProposalStatus = "applied"  // approved and successfully applied by the server
	ProposalFailed   ProposalStatus = "failed"   // approved but the apply errored
)

// Proposal is a single agent-generated remediation: a change to one resource
// that a human must approve before the server applies it. The agent NEVER
// applies a proposal itself — it has no write RBAC. See docs/security.md.
type Proposal struct {
	ID        string         `json:"id"`
	CreatedAt time.Time      `json:"createdAt"`
	Status    ProposalStatus `json:"status"`

	// Target resource (matches the resource-browser addressing).
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`

	Rationale    string `json:"rationale"`    // why the agent proposes this
	CurrentYAML  string `json:"currentYaml"`  // snapshot at proposal time, for the diff
	ProposedYAML string `json:"proposedYaml"` // the manifest to apply on approval

	DecidedAt *time.Time `json:"decidedAt,omitempty"` // when approved/rejected
	Error     string     `json:"error,omitempty"`     // apply failure detail
}

func newProposalID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// createProposalsTable is folded into the store schema (see openInsightDB).
const proposalsSchema = `
	CREATE TABLE IF NOT EXISTS proposals (
		id TEXT PRIMARY KEY,
		created_at TEXT NOT NULL,
		status TEXT NOT NULL,
		kind TEXT NOT NULL,
		namespace TEXT NOT NULL,
		name TEXT NOT NULL,
		rationale TEXT NOT NULL DEFAULT '',
		current_yaml TEXT NOT NULL DEFAULT '',
		proposed_yaml TEXT NOT NULL,
		decided_at TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_proposals_status ON proposals(status, created_at);`

// SaveProposal inserts a new pending proposal and returns it with its ID and
// timestamp set. Returns an error (not a silent no-op like Add) because the
// caller — a tool the LLM invoked — needs to report success/failure back.
func (s *Store) SaveProposal(p Proposal) (Proposal, error) {
	if s.db == nil {
		return Proposal{}, errors.New("proposals require a persistent database (set INSIGHT_DB_PATH)")
	}
	p.ID = newProposalID()
	p.CreatedAt = time.Now().UTC()
	p.Status = ProposalPending
	_, err := s.db.Exec(
		`INSERT INTO proposals (id, created_at, status, kind, namespace, name, rationale, current_yaml, proposed_yaml)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.CreatedAt.Format(time.RFC3339Nano), string(p.Status),
		p.Kind, p.Namespace, p.Name, p.Rationale, p.CurrentYAML, p.ProposedYAML,
	)
	if err != nil {
		return Proposal{}, fmt.Errorf("saving proposal: %w", err)
	}
	return p, nil
}

// ListProposals returns proposals newest-first. When onlyPending is true only
// pending ones are returned (the actionable set for the UI badge).
func (s *Store) ListProposals(onlyPending bool) ([]Proposal, error) {
	if s.db == nil {
		return nil, nil
	}
	q := `SELECT id, created_at, status, kind, namespace, name, rationale, current_yaml, proposed_yaml, decided_at, error
	      FROM proposals`
	if onlyPending {
		q += ` WHERE status = 'pending'`
	}
	q += ` ORDER BY created_at DESC LIMIT 200`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("listing proposals: %w", err)
	}
	defer rows.Close()

	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProposalsSince returns proposals created OR decided within the window,
// newest first — the daily report's "what changed" section.
func (s *Store) ProposalsSince(since time.Time) ([]Proposal, error) {
	if s.db == nil {
		return nil, nil
	}
	cutoff := since.UTC().Format(time.RFC3339Nano)
	rows, err := s.db.Query(
		`SELECT id, created_at, status, kind, namespace, name, rationale, current_yaml, proposed_yaml, decided_at, error
		 FROM proposals WHERE created_at >= ? OR (decided_at != '' AND decided_at >= ?)
		 ORDER BY created_at DESC LIMIT 100`, cutoff, cutoff)
	if err != nil {
		return nil, fmt.Errorf("listing recent proposals: %w", err)
	}
	defer rows.Close()

	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProposal returns one proposal by ID.
func (s *Store) GetProposal(id string) (Proposal, bool, error) {
	if s.db == nil {
		return Proposal{}, false, nil
	}
	row := s.db.QueryRow(
		`SELECT id, created_at, status, kind, namespace, name, rationale, current_yaml, proposed_yaml, decided_at, error
		 FROM proposals WHERE id = ?`, id)
	p, err := scanProposal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Proposal{}, false, nil
	}
	if err != nil {
		return Proposal{}, false, err
	}
	return p, true, nil
}

// scanner is the shared interface of *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanProposal(row scanner) (Proposal, error) {
	var p Proposal
	var createdAt, status, decidedAt string
	if err := row.Scan(&p.ID, &createdAt, &status, &p.Kind, &p.Namespace, &p.Name,
		&p.Rationale, &p.CurrentYAML, &p.ProposedYAML, &decidedAt, &p.Error); err != nil {
		return Proposal{}, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	p.Status = ProposalStatus(status)
	if decidedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, decidedAt); err == nil {
			p.DecidedAt = &t
		}
	}
	return p, nil
}

// decideProposal transitions a pending proposal to a terminal status. It only
// succeeds from pending, so a proposal can't be applied twice or applied after
// rejection (guards double-apply and approve-after-reject races).
func (s *Store) decideProposal(id string, status ProposalStatus, applyErr string) error {
	if s.db == nil {
		return errors.New("no database")
	}
	res, err := s.db.Exec(
		`UPDATE proposals SET status = ?, decided_at = ?, error = ?
		 WHERE id = ? AND status = 'pending'`,
		string(status), time.Now().UTC().Format(time.RFC3339Nano), applyErr, id)
	if err != nil {
		return fmt.Errorf("updating proposal: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("proposal %s is not pending (already decided or unknown)", id)
	}
	return nil
}

// RejectProposal marks a pending proposal rejected (no cluster action).
func (s *Store) RejectProposal(id string) error {
	return s.decideProposal(id, ProposalRejected, "")
}

// ResolveProposal records the outcome of an approved+applied proposal. Called
// by the server after it applies (or fails to apply) the change — the agent
// itself never applies. applyErr empty = success.
func (s *Store) ResolveProposal(id string, applyErr string) error {
	if applyErr != "" {
		return s.decideProposal(id, ProposalFailed, applyErr)
	}
	return s.decideProposal(id, ProposalApplied, "")
}
