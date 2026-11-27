// Package presence keeps how often gnockpit's polls listed each peer. Per chain
// and UTC day it stores the poll count and each node ID's listings, for 35
// days (the 30-day window the figures cover, plus a margin). Each node ID's
// first listing and the chain's first poll are kept for good.
package presence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

const (
	windowDays = 30
	keepDays   = 35
)

const dayLayout = "2006-01-02"

const schema = `
CREATE TABLE IF NOT EXISTS presence_polls (
	chain TEXT NOT NULL,
	day   TEXT NOT NULL, -- YYYY-MM-DD, UTC
	polls INTEGER NOT NULL,
	PRIMARY KEY (chain, day)
);
CREATE TABLE IF NOT EXISTS presence_listed (
	chain   TEXT NOT NULL,
	day     TEXT NOT NULL,
	node_id TEXT NOT NULL,
	listed  INTEGER NOT NULL,
	PRIMARY KEY (chain, day, node_id)
);
CREATE TABLE IF NOT EXISTS presence_first (
	chain    TEXT NOT NULL,
	node_id  TEXT NOT NULL,
	first_ms INTEGER NOT NULL,
	PRIMARY KEY (chain, node_id)
);
CREATE TABLE IF NOT EXISTS presence_start (
	chain      TEXT PRIMARY KEY,
	started_ms INTEGER NOT NULL
);
`

// Store persists peer presence. Times are kept to the millisecond.
type Store struct {
	db *sql.DB

	mu        sync.Mutex
	prunedDay string // the UTC day of the last prune, empty before the first
}

// NewStore initializes the schema on db, which may be shared with other
// stores.
func NewStore(ctx context.Context, db *sql.DB) (*Store, error) {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("init presence schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Record stores one poll of chain at at: the poll counts on at's UTC day, and
// each distinct non-empty node ID in nodeIDs is counted as listed once. The
// first Record of a UTC day deletes the per-day rows older than keepDays, for
// every chain.
func (s *Store) Record(ctx context.Context, chain string, at time.Time, nodeIDs []string) error {
	at = at.UTC()
	day := at.Format(dayLayout)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO presence_polls(chain, day, polls) VALUES(?, ?, 1)
		 ON CONFLICT(chain, day) DO UPDATE SET polls = polls + 1`, chain, day); err != nil {
		return fmt.Errorf("count poll: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO presence_start(chain, started_ms) VALUES(?, ?)`, chain, at.UnixMilli()); err != nil {
		return fmt.Errorf("keep first poll: %w", err)
	}
	seen := make(map[string]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO presence_listed(chain, day, node_id, listed) VALUES(?, ?, ?, 1)
			 ON CONFLICT(chain, day, node_id) DO UPDATE SET listed = listed + 1`, chain, day, id); err != nil {
			return fmt.Errorf("count listing of %s: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO presence_first(chain, node_id, first_ms) VALUES(?, ?, ?)`,
			chain, id, at.UnixMilli()); err != nil {
			return fmt.Errorf("keep first listing of %s: %w", id, err)
		}
	}

	// The lock is held through the commit so prunedDay only records a prune that
	// was committed, and two Records never both prune the same day.
	s.mu.Lock()
	defer s.mu.Unlock()
	prune := day > s.prunedDay
	if prune {
		oldest := at.AddDate(0, 0, -(keepDays - 1)).Format(dayLayout)
		for _, table := range []string{"presence_polls", "presence_listed"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE day < ?`, oldest); err != nil {
				return fmt.Errorf("prune %s: %w", table, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit poll: %w", err)
	}
	if prune {
		s.prunedDay = day
	}
	return nil
}

// Figures returns the presence of the nodeIDs on chain over the window: now's
// UTC day and the 29 before. A node ID has an entry when the window holds a
// poll and the node has been listed at some time (Listed may be 0). With no
// poll in the window the map is empty.
func (s *Store) Figures(ctx context.Context, chain string, now time.Time, nodeIDs []string) (map[string]node.Presence, error) {
	out := make(map[string]node.Presence, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return out, nil
	}
	now = now.UTC()
	to := now.Format(dayLayout)
	from := now.AddDate(0, 0, -(windowDays - 1)).Format(dayLayout)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var polls int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(polls), 0) FROM presence_polls WHERE chain = ? AND day BETWEEN ? AND ?`,
		chain, from, to).Scan(&polls); err != nil {
		return nil, fmt.Errorf("read polls: %w", err)
	}
	if polls == 0 {
		return out, nil
	}

	var startedMs int64
	if err := tx.QueryRowContext(ctx,
		`SELECT started_ms FROM presence_start WHERE chain = ?`, chain).Scan(&startedMs); err != nil {
		return nil, fmt.Errorf("read first poll: %w", err)
	}

	marks := strings.TrimSuffix(strings.Repeat("?,", len(nodeIDs)), ",")
	idArgs := make([]any, len(nodeIDs))
	for i, id := range nodeIDs {
		idArgs[i] = id
	}
	listed := make(map[string]int, len(nodeIDs))
	rows, err := tx.QueryContext(ctx,
		`SELECT node_id, SUM(listed) FROM presence_listed
		 WHERE chain = ? AND day BETWEEN ? AND ? AND node_id IN (`+marks+`) GROUP BY node_id`, append([]any{chain, from, to}, idArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("read listings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan listings: %w", err)
		}
		listed[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read listings: %w", err)
	}

	firsts, err := tx.QueryContext(ctx,
		`SELECT node_id, first_ms FROM presence_first WHERE chain = ? AND node_id IN (`+marks+`)`, append([]any{chain}, idArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("read first listings: %w", err)
	}
	defer firsts.Close()
	for firsts.Next() {
		var id string
		var ms int64
		if err := firsts.Scan(&id, &ms); err != nil {
			return nil, fmt.Errorf("scan first listings: %w", err)
		}
		out[id] = node.Presence{
			Listed:    listed[id],
			Polls:     polls,
			FirstSeen: time.UnixMilli(ms).UTC().Format(time.RFC3339),
			FromStart: ms == startedMs,
		}
	}
	if err := firsts.Err(); err != nil {
		return nil, fmt.Errorf("read first listings: %w", err)
	}
	return out, nil
}
