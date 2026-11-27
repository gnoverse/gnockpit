package location

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"
	"time"
)

const schema = `
CREATE TABLE IF NOT EXISTS location_declarations (
	chain    TEXT,
	operator TEXT,
	at_ns    INTEGER NOT NULL, -- the record's time, in nanoseconds since 1970
	clear    INTEGER NOT NULL, -- 1 for a removal
	lat      REAL,             -- NULL for a removal
	lon      REAL,             -- NULL for a removal
	country  TEXT NOT NULL,
	provider TEXT NOT NULL,
	signed   TEXT NOT NULL,
	PRIMARY KEY (chain, operator)
);
`

// ErrNotNewer refuses a declaration that is not newer than the one stored for
// its operator.
var ErrNotNewer = errors.New("a declaration as recent or more recent is stored")

// The times a row holds: nanoseconds since 1970 in an int64.
var (
	minAt = time.Unix(0, math.MinInt64).UTC()
	maxAt = time.Unix(0, math.MaxInt64).UTC()
)

// writeTimeout bounds how long a Put waits for a connection to the database.
const writeTimeout = 10 * time.Second

// Store keeps each operator's latest declaration per chain, in the table
// location_declarations and in memory. A removal replaces the operator's
// declaration as a tombstone.
type Store struct {
	db *sql.DB

	// write is the store's mutex: a Put holds it through its check against
	// the stored declaration, its write and the update of byChain, so a
	// declaration stores only when newer than every one stored before.
	write sync.Mutex

	// mu guards byChain alone and is never held across the database, so Get
	// and All never wait on a write.
	mu sync.RWMutex
	// byChain is every stored declaration, by chain then operator.
	byChain map[string]map[string]Declaration
}

// NewStore initializes the schema on db, which may be shared with other
// stores, and reads every stored declaration.
func NewStore(ctx context.Context, db *sql.DB) (*Store, error) {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("init location declarations schema: %w", err)
	}
	s := &Store{db: db, byChain: map[string]map[string]Declaration{}}
	rows, err := db.QueryContext(ctx,
		`SELECT chain, operator, at_ns, clear, lat, lon, country, provider, signed FROM location_declarations`)
	if err != nil {
		return nil, fmt.Errorf("read location declarations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			d        Declaration
			atNs     int64
			lat, lon sql.NullFloat64
		)
		if err := rows.Scan(&d.Chain, &d.Operator, &atNs, &d.Clear, &lat, &lon, &d.Country, &d.Provider, &d.Signed); err != nil {
			return nil, fmt.Errorf("read location declarations: %w", err)
		}
		d.At = time.Unix(0, atNs).UTC()
		d.Lat, d.Lon = lat.Float64, lon.Float64
		s.keep(d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read location declarations: %w", err)
	}
	return s, nil
}

// keep puts d in memory; the caller holds mu or has the store to itself.
func (s *Store) keep(d Declaration) {
	byOperator := s.byChain[d.Chain]
	if byOperator == nil {
		byOperator = map[string]Declaration{}
		s.byChain[d.Chain] = byOperator
	}
	byOperator[d.Operator] = d
}

// Put stores d as its operator's declaration on its chain, replacing the
// stored one, under the store's mutex. It refuses, with ErrNotNewer, a d whose
// time is not after the stored one's: a removal stays until a newer
// declaration replaces it. It also refuses a d whose time is out of the
// store's range, the times an int64 of nanoseconds since 1970 holds. It waits
// at most writeTimeout for a connection to the database; the write itself
// runs to its end whatever ctx does, since a write interrupted as it commits
// is stored yet reported failed.
func (s *Store) Put(ctx context.Context, d Declaration) (err error) {
	if d.At.Before(minAt) || d.At.After(maxAt) {
		return fmt.Errorf("the record's time %s is out of the store's range, %s to %s",
			d.At.Format(time.RFC3339Nano), minAt.Format(time.RFC3339Nano), maxAt.Format(time.RFC3339Nano))
	}
	s.write.Lock()
	defer s.write.Unlock()
	if stored, ok := s.Get(d.Chain, d.Operator); ok && !stored.At.Before(d.At) {
		return fmt.Errorf("%w for %s (at %s)", ErrNotNewer, d.Operator, stored.At.Format(time.RFC3339Nano))
	}
	wait, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	conn, err := s.db.Conn(wait)
	if err != nil {
		return fmt.Errorf("store the declaration of %s on %s: %w", d.Operator, d.Chain, err)
	}
	// A connection the pool cannot take back is reported, the declaration
	// stored and mirrored all the same.
	defer func() { err = errors.Join(err, conn.Close()) }()
	var lat, lon sql.NullFloat64
	if !d.Clear {
		lat = sql.NullFloat64{Float64: d.Lat, Valid: true}
		lon = sql.NullFloat64{Float64: d.Lon, Valid: true}
	}
	if _, err := conn.ExecContext(context.WithoutCancel(ctx),
		`INSERT OR REPLACE INTO location_declarations(chain, operator, at_ns, clear, lat, lon, country, provider, signed)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.Chain, d.Operator, d.At.UnixNano(), d.Clear, lat, lon, d.Country, d.Provider, d.Signed); err != nil {
		return fmt.Errorf("store the declaration of %s on %s: %w", d.Operator, d.Chain, err)
	}
	s.mu.Lock()
	s.keep(d)
	s.mu.Unlock()
	return nil
}

// Get returns operator's stored declaration on chain, false when none is.
func (s *Store) Get(chain, operator string) (Declaration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.byChain[chain][operator]
	return d, ok
}

// All returns every declaration stored on chain, removals included, by
// operator.
func (s *Store) All(chain string) []Declaration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.SortedFunc(maps.Values(s.byChain[chain]), func(a, b Declaration) int {
		return cmp.Compare(a.Operator, b.Operator)
	})
}
