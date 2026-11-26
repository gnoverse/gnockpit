package ranking

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Published is the ranking published for a chain's UTC day: Result, over the
// 30 complete UTC days before it.
type Published struct {
	Chain      string    `json:"chain"` // the chain id
	Day        string    `json:"day"`   // YYYY-MM-DD, UTC
	ComputedAt time.Time `json:"computed_at"`
	Exclusions []string  `json:"exclusions"` // the operators excluded, sorted, each once
	Result
}

// daysSchema keeps each chain's published days once each, as the JSON of
// Published; the formula and the exclusion list, comma-joined, beside it.
const daysSchema = `
CREATE TABLE IF NOT EXISTS ranking_days (
	chain      TEXT NOT NULL,
	day        TEXT NOT NULL, -- YYYY-MM-DD, UTC: the days order as text
	formula    INTEGER NOT NULL,
	exclusions TEXT NOT NULL,
	published  TEXT NOT NULL,
	PRIMARY KEY (chain, day)
);
`

// DayStore keeps each day the ranking is published for, by chain: a chain
// reads its own days only.
type DayStore struct {
	db *sql.DB
}

// NewDayStore initializes the schema on db, which may be shared with other
// stores.
func NewDayStore(ctx context.Context, db *sql.DB) (*DayStore, error) {
	if _, err := db.ExecContext(ctx, daysSchema); err != nil {
		return nil, fmt.Errorf("init ranking days schema: %w", err)
	}
	return &DayStore{db: db}, nil
}

// Put stores p, replacing the day stored for its Chain and Day. Its Chain
// must be set, and its Day a date, YYYY-MM-DD: the days order as text.
func (s *DayStore) Put(ctx context.Context, p Published) error {
	if p.Chain == "" {
		return fmt.Errorf("ranking day %s: no chain", p.Day)
	}
	if _, err := time.Parse(time.DateOnly, p.Day); err != nil {
		return fmt.Errorf("ranking day %q of %s: %w", p.Day, p.Chain, err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode ranking day %s of %s: %w", p.Day, p.Chain, err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO ranking_days(chain, day, formula, exclusions, published) VALUES(?, ?, ?, ?, ?)`,
		p.Chain, p.Day, p.Formula, strings.Join(p.Exclusions, ","), string(b)); err != nil {
		return fmt.Errorf("put ranking day %s of %s: %w", p.Day, p.Chain, err)
	}
	return nil
}

// Latest returns chain's latest stored day, false when none is.
func (s *DayStore) Latest(ctx context.Context, chain string) (Published, bool, error) {
	return s.read(ctx, `SELECT day, published FROM ranking_days WHERE chain = ? ORDER BY day DESC LIMIT 1`, chain)
}

// Previous returns chain's latest stored day before day, false when none is.
func (s *DayStore) Previous(ctx context.Context, chain, day string) (Published, bool, error) {
	return s.read(ctx, `SELECT day, published FROM ranking_days WHERE chain = ? AND day < ? ORDER BY day DESC LIMIT 1`, chain, day)
}

// read returns the stored day query selects, false when it selects none.
func (s *DayStore) read(ctx context.Context, query string, args ...any) (Published, bool, error) {
	var day, raw string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&day, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Published{}, false, nil
	}
	if err != nil {
		return Published{}, false, fmt.Errorf("read ranking day: %w", err)
	}
	var p Published
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Published{}, false, fmt.Errorf("decode ranking day %s: %w", day, err)
	}
	return p, true, nil
}
