// Package blocks records what the chain did at every height since genesis:
// each block's time, proposer and round, the validator set that signed it,
// which members did not sign it for the block and how, the vote delays, the
// expected proposer of each round of a late block, and the events that changed
// the set. It keeps them in SQLite, filled from an RPC source, as the facts
// missed-block counts and validator scores are computed from.
package blocks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// The schema drops signing_blocks and signing_misses where a database has
// them: blocks_absences holds the same per-block misses, back to genesis.
const schema = `
CREATE TABLE IF NOT EXISTS blocks (
	height  INTEGER PRIMARY KEY,
	time_ms INTEGER NOT NULL,
	proposer TEXT NOT NULL,
	round   INTEGER NOT NULL,
	txs     INTEGER NOT NULL,
	valset  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_blocks_time ON blocks(time_ms);
CREATE TABLE IF NOT EXISTS blocks_valsets (
	id   INTEGER PRIMARY KEY,
	hash TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS blocks_valset_members (
	valset  INTEGER NOT NULL,
	idx     INTEGER NOT NULL,
	address TEXT NOT NULL,
	pub_key TEXT NOT NULL,
	power   INTEGER NOT NULL,
	PRIMARY KEY (valset, idx)
);
CREATE TABLE IF NOT EXISTS blocks_absences (
	height  INTEGER NOT NULL,
	address TEXT NOT NULL,
	kind    INTEGER NOT NULL,
	PRIMARY KEY (height, address)
);
CREATE INDEX IF NOT EXISTS idx_blocks_absences_address ON blocks_absences(address, height);
CREATE TABLE IF NOT EXISTS blocks_rounds (
	height   INTEGER NOT NULL,
	round    INTEGER NOT NULL,
	proposer TEXT NOT NULL,
	PRIMARY KEY (height, round)
);
CREATE TABLE IF NOT EXISTS blocks_flags (
	height INTEGER PRIMARY KEY,
	reason TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS blocks_events (
	height        INTEGER NOT NULL,
	idx           INTEGER NOT NULL,
	source_height INTEGER NOT NULL,
	type          TEXT NOT NULL,
	operator      TEXT NOT NULL,
	address       TEXT NOT NULL,
	old_address   TEXT NOT NULL,
	new_address   TEXT NOT NULL,
	power         INTEGER NOT NULL,
	applied       INTEGER NOT NULL,
	PRIMARY KEY (height, idx)
);
CREATE TABLE IF NOT EXISTS blocks_vote_delays (
	day     INTEGER NOT NULL,
	address TEXT NOT NULL,
	votes   INTEGER NOT NULL,
	sum_ms  INTEGER NOT NULL,
	b0 INTEGER NOT NULL, b1 INTEGER NOT NULL, b2 INTEGER NOT NULL, b3 INTEGER NOT NULL,
	b4 INTEGER NOT NULL, b5 INTEGER NOT NULL, b6 INTEGER NOT NULL,
	PRIMARY KEY (day, address)
);
CREATE TABLE IF NOT EXISTS blocks_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
DROP TABLE IF EXISTS signing_misses;
DROP TABLE IF EXISTS signing_blocks;
`

const (
	metaChainID = "chain_id"
	metaFloor   = "floor"
)

// Store persists the block history. Times are kept to the millisecond.
type Store struct {
	db *sql.DB
}

// NewStore initializes the schema on db, which may be shared with other
// stores.
func NewStore(ctx context.Context, db *sql.DB) (*Store, error) {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("init block history schema: %w", err)
	}
	return &Store{db: db}, nil
}

// ---- Validator sets

// PutValset stores the members of the validator set with hash, unless it is
// stored.
func (s *Store) PutValset(ctx context.Context, hash string, members []Member) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO blocks_valsets(hash) VALUES(?)`, hash)
	if err != nil {
		return fmt.Errorf("insert validator set %s: %w", hash, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("validator set %s id: %w", hash, err)
	}
	for i, m := range members {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO blocks_valset_members(valset, idx, address, pub_key, power) VALUES(?, ?, ?, ?, ?)`,
			id, i, m.Address, m.PubKey, m.Power,
		); err != nil {
			return fmt.Errorf("insert member %d of validator set %s: %w", i, hash, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit validator set %s: %w", hash, err)
	}
	return nil
}

// Valset returns the members of the stored validator set with hash, in set
// order.
func (s *Store) Valset(ctx context.Context, hash string) ([]Member, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.address, m.pub_key, m.power FROM blocks_valset_members m
		 JOIN blocks_valsets v ON v.id = m.valset WHERE v.hash = ? ORDER BY m.idx`, hash)
	if err != nil {
		return nil, false, fmt.Errorf("query validator set %s: %w", hash, err)
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.Address, &m.PubKey, &m.Power); err != nil {
			return nil, false, fmt.Errorf("scan member: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate members: %w", err)
	}
	return out, len(out) > 0, nil
}

// ValsetHashAt returns the hash of the validator set of the stored height.
func (s *Store) ValsetHashAt(ctx context.Context, height int64) (string, bool, error) {
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT v.hash FROM blocks b JOIN blocks_valsets v ON v.id = b.valset WHERE b.height = ?`, height,
	).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("validator set at %d: %w", height, err)
	}
	return hash, true, nil
}

// ---- Blocks

// Write stores records in one transaction. A record's validator set must be
// stored first. A height already stored keeps its rows: its vote delays are
// counted once.
func (s *Store) Write(ctx context.Context, recs []Record) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	for _, r := range recs {
		if err := writeRecord(ctx, tx, r); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit blocks: %w", err)
	}
	return nil
}

func writeRecord(ctx context.Context, tx *sql.Tx, r Record) error {
	var valset int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM blocks_valsets WHERE hash = ?`, r.ValsetHash).Scan(&valset); err != nil {
		return fmt.Errorf("height %d: validator set %s: %w", r.Height, r.ValsetHash, err)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO blocks(height, time_ms, proposer, round, txs, valset) VALUES(?, ?, ?, ?, ?, ?)`,
		r.Height, r.Time.UnixMilli(), r.Proposer, r.Round, r.Txs, valset)
	if err != nil {
		return fmt.Errorf("insert block %d: %w", r.Height, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	for _, a := range r.Absences {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO blocks_absences(height, address, kind) VALUES(?, ?, ?)`, r.Height, a.Address, a.Kind,
		); err != nil {
			return fmt.Errorf("insert absence %d/%s: %w", r.Height, a.Address, err)
		}
	}
	for round, p := range r.Rounds {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO blocks_rounds(height, round, proposer) VALUES(?, ?, ?)`, r.Height, round, p,
		); err != nil {
			return fmt.Errorf("insert round %d/%d: %w", r.Height, round, err)
		}
	}
	if r.Flag != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO blocks_flags(height, reason) VALUES(?, ?)`, r.Height, r.Flag,
		); err != nil {
			return fmt.Errorf("insert flag %d: %w", r.Height, err)
		}
	}
	day := dayOf(r.Time)
	for _, d := range r.Delays {
		var b [DelayBuckets]int64
		b[delayBucket(d.Ms)] = 1
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO blocks_vote_delays(day, address, votes, sum_ms, b0, b1, b2, b3, b4, b5, b6)
			 VALUES(?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(day, address) DO UPDATE SET
			 votes = votes + 1, sum_ms = sum_ms + excluded.sum_ms,
			 b0 = b0 + excluded.b0, b1 = b1 + excluded.b1, b2 = b2 + excluded.b2, b3 = b3 + excluded.b3,
			 b4 = b4 + excluded.b4, b5 = b5 + excluded.b5, b6 = b6 + excluded.b6`,
			day, d.Address, d.Ms, b[0], b[1], b[2], b[3], b[4], b[5], b[6],
		); err != nil {
			return fmt.Errorf("count delay %d/%s: %w", r.Height, d.Address, err)
		}
	}
	return nil
}

// Range returns the lowest and highest stored heights, 0 and 0 when none is.
func (s *Store) Range(ctx context.Context) (low, high int64, err error) {
	var lo, hi sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(height), MAX(height) FROM blocks`).Scan(&lo, &hi); err != nil {
		return 0, 0, fmt.Errorf("block range: %w", err)
	}
	return lo.Int64, hi.Int64, nil
}

// TimeAt returns the time of the stored height.
func (s *Store) TimeAt(ctx context.Context, height int64) (time.Time, bool, error) {
	var ms int64
	err := s.db.QueryRowContext(ctx, `SELECT time_ms FROM blocks WHERE height = ?`, height).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("time of %d: %w", height, err)
	}
	return time.UnixMilli(ms).UTC(), true, nil
}

// Earliest returns the time of the lowest stored height, or the zero time
// when none is.
func (s *Store) Earliest(ctx context.Context) (time.Time, error) {
	var ms int64
	err := s.db.QueryRowContext(ctx, `SELECT time_ms FROM blocks ORDER BY height LIMIT 1`).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("earliest block: %w", err)
	}
	return time.UnixMilli(ms).UTC(), nil
}

// Span is what the store holds for a span of heights.
type Span struct {
	Blocks  []Record            // ascending, with absences, expected round proposers and flags
	Sets    map[string][]Member // the validator sets the blocks name, by hash
	Changes map[int64]SetChange // the validator set changes in the span, by height
}

// SetChange is the events behind a validator set change, read from the block
// at Source.
type SetChange struct {
	Source int64
	Events []Event
}

// Blocks returns what the store holds for heights from to to.
func (s *Store) Blocks(ctx context.Context, from, to int64) (Span, error) {
	span := Span{Sets: map[string][]Member{}, Changes: map[int64]SetChange{}}
	index := map[int64]int{}
	err := s.each(ctx, func(rows *sql.Rows) error {
		var r Record
		var ms int64
		if err := rows.Scan(&r.Height, &ms, &r.Proposer, &r.Round, &r.Txs, &r.ValsetHash); err != nil {
			return err
		}
		r.Time = time.UnixMilli(ms).UTC()
		index[r.Height] = len(span.Blocks)
		span.Blocks = append(span.Blocks, r)
		return nil
	}, `SELECT b.height, b.time_ms, b.proposer, b.round, b.txs, v.hash FROM blocks b
	    JOIN blocks_valsets v ON v.id = b.valset WHERE b.height BETWEEN ? AND ? ORDER BY b.height`, from, to)
	if err != nil {
		return Span{}, fmt.Errorf("blocks %d-%d: %w", from, to, err)
	}
	err = s.each(ctx, func(rows *sql.Rows) error {
		var h int64
		var a Absence
		if err := rows.Scan(&h, &a.Address, &a.Kind); err != nil {
			return err
		}
		if i, ok := index[h]; ok {
			span.Blocks[i].Absences = append(span.Blocks[i].Absences, a)
		}
		return nil
	}, `SELECT height, address, kind FROM blocks_absences WHERE height BETWEEN ? AND ? ORDER BY height, rowid`, from, to)
	if err != nil {
		return Span{}, fmt.Errorf("absences %d-%d: %w", from, to, err)
	}
	err = s.each(ctx, func(rows *sql.Rows) error {
		var h int64
		var p string
		if err := rows.Scan(&h, &p); err != nil {
			return err
		}
		if i, ok := index[h]; ok {
			span.Blocks[i].Rounds = append(span.Blocks[i].Rounds, p)
		}
		return nil
	}, `SELECT height, proposer FROM blocks_rounds WHERE height BETWEEN ? AND ? ORDER BY height, round`, from, to)
	if err != nil {
		return Span{}, fmt.Errorf("rounds %d-%d: %w", from, to, err)
	}
	err = s.each(ctx, func(rows *sql.Rows) error {
		var h int64
		var reason string
		if err := rows.Scan(&h, &reason); err != nil {
			return err
		}
		if i, ok := index[h]; ok {
			span.Blocks[i].Flag = reason
		}
		return nil
	}, `SELECT height, reason FROM blocks_flags WHERE height BETWEEN ? AND ?`, from, to)
	if err != nil {
		return Span{}, fmt.Errorf("flags %d-%d: %w", from, to, err)
	}
	err = s.each(ctx, func(rows *sql.Rows) error {
		var h int64
		var source int64
		var e Event
		if err := rows.Scan(&h, &source, &e.Type, &e.Operator, &e.Address, &e.OldAddress, &e.NewAddress, &e.Power, &e.Applied); err != nil {
			return err
		}
		change := span.Changes[h]
		change.Source = source
		change.Events = append(change.Events, e)
		span.Changes[h] = change
		return nil
	}, `SELECT height, source_height, type, operator, address, old_address, new_address, power, applied
	    FROM blocks_events WHERE height BETWEEN ? AND ? ORDER BY height, idx`, from, to)
	if err != nil {
		return Span{}, fmt.Errorf("events %d-%d: %w", from, to, err)
	}
	for _, r := range span.Blocks {
		if _, ok := span.Sets[r.ValsetHash]; ok {
			continue
		}
		members, _, err := s.Valset(ctx, r.ValsetHash)
		if err != nil {
			return Span{}, err
		}
		span.Sets[r.ValsetHash] = members
	}
	return span, nil
}

// each runs query and calls scan on each row.
func (s *Store) each(ctx context.Context, scan func(*sql.Rows) error, query string, args ...any) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ---- Vote delays

// DelayBuckets is how many buckets the vote delay histogram has.
const DelayBuckets = len(delayEdges) + 1

// delayEdges bound the vote delay histogram's buckets, in ms. A delay on an
// edge falls in the bucket farther from 0.
var delayEdges = [...]int64{-1000, -250, 250, 1000, 2500, 5000}

// DelayEdges returns the edges of the vote delay histogram's buckets, in ms:
// bucket i holds the delays between edges i-1 and i, a delay on an edge
// falling in the bucket farther from 0.
func DelayEdges() [DelayBuckets - 1]int64 { return delayEdges }

// delayBucket is the histogram bucket of a vote delay in ms.
func delayBucket(ms int64) int {
	for i, e := range delayEdges {
		if ms < e || ms < 0 && ms == e {
			return i
		}
	}
	return len(delayEdges)
}

// DayDelays is a validator's vote delays over a UTC day.
type DayDelays struct {
	Day     time.Time // midnight UTC
	Address string
	Votes   int64
	SumMs   int64
	Buckets [DelayBuckets]int64 // see DelayEdges
}

// VoteDelays returns the vote delays of the UTC days from from to to, by day
// then address.
func (s *Store) VoteDelays(ctx context.Context, from, to time.Time) ([]DayDelays, error) {
	var out []DayDelays
	err := s.each(ctx, func(rows *sql.Rows) error {
		var d DayDelays
		var day int64
		b := &d.Buckets
		if err := rows.Scan(&day, &d.Address, &d.Votes, &d.SumMs, &b[0], &b[1], &b[2], &b[3], &b[4], &b[5], &b[6]); err != nil {
			return err
		}
		d.Day = time.Unix(day*secondsPerDay, 0).UTC()
		out = append(out, d)
		return nil
	}, `SELECT day, address, votes, sum_ms, b0, b1, b2, b3, b4, b5, b6 FROM blocks_vote_delays
	    WHERE day BETWEEN ? AND ? ORDER BY day, address`, dayOf(from), dayOf(to))
	if err != nil {
		return nil, fmt.Errorf("vote delays: %w", err)
	}
	return out, nil
}

// secondsPerDay is a UTC day's length: vote delays are counted per day.
const secondsPerDay = 24 * 60 * 60

// dayOf is the UTC day of t, in days since the Unix epoch.
func dayOf(t time.Time) int64 { return t.UTC().Unix() / secondsPerDay }

// ---- Missed-block windows

// windowMissesQuery counts each validator's absent and other-block precommits
// from a height on. The unary + keeps SQLite from grouping through the address
// index, a pass over every stored absence; it searches the window by height.
const windowMissesQuery = `SELECT address, COUNT(*) FROM blocks_absences WHERE height >= ? AND kind IN (?, ?) GROUP BY +address`

// WindowReport holds missed-block counts across windows: Blocks[i] and
// Missed[addr][i] correspond to Windows[i].
type WindowReport struct {
	Windows []time.Duration
	Blocks  []int
	Missed  map[string][]int
}

// MissedByWindows returns, for each window up to now, the stored blocks and
// each validator's missed ones: absent, or precommitted for another block. A
// window of zero or less means all the history.
func (s *Store) MissedByWindows(ctx context.Context, windows []time.Duration, now time.Time) (WindowReport, error) {
	rep := WindowReport{Windows: windows, Blocks: make([]int, len(windows)), Missed: map[string][]int{}}
	for i, w := range windows {
		var cutoff int64
		if w > 0 {
			cutoff = now.Add(-w).UnixMilli()
		}
		var from sql.NullInt64
		if err := s.db.QueryRowContext(ctx,
			`SELECT MIN(height), COUNT(*) FROM blocks WHERE time_ms >= ?`, cutoff,
		).Scan(&from, &rep.Blocks[i]); err != nil {
			return rep, fmt.Errorf("count blocks: %w", err)
		}
		if !from.Valid {
			continue
		}
		rows, err := s.db.QueryContext(ctx, windowMissesQuery, from.Int64, Absent, OtherBlock)
		if err != nil {
			return rep, fmt.Errorf("query misses: %w", err)
		}
		for rows.Next() {
			var addr string
			var n int
			if err := rows.Scan(&addr, &n); err != nil {
				rows.Close()
				return rep, fmt.Errorf("scan miss: %w", err)
			}
			if rep.Missed[addr] == nil {
				rep.Missed[addr] = make([]int, len(windows))
			}
			rep.Missed[addr][i] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return rep, fmt.Errorf("iterate misses: %w", err)
		}
	}
	return rep, nil
}

// ---- Events

// Event is a validator set event a block's transactions emitted.
type Event struct {
	Type       string // ValidatorAdded, ValidatorRemoved or ValoperRotated
	Operator   string
	Address    string // the signing address added or removed
	OldAddress string // a rotation's
	NewAddress string // a rotation's
	Power      int64
	Applied    bool // the change reached the set; false for a rotation of an operator outside it
}

// PutEvents stores the events that changed the validator set at height, read
// from the block at source, unless they are stored.
func (s *Store) PutEvents(ctx context.Context, height, source int64, events []Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	for i, e := range events {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO blocks_events(height, idx, source_height, type, operator, address, old_address, new_address, power, applied)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			height, i, source, e.Type, e.Operator, e.Address, e.OldAddress, e.NewAddress, e.Power, e.Applied,
		); err != nil {
			return fmt.Errorf("insert event %d/%d: %w", height, i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit events: %w", err)
	}
	return nil
}

// ---- Meta

// ChainID returns the chain the stored history belongs to, or "" before Reset.
func (s *Store) ChainID(ctx context.Context) (string, error) {
	return s.meta(ctx, metaChainID)
}

// Floor returns the lowest height the source serves, recorded when it refused
// a lower one, or 0.
func (s *Store) Floor(ctx context.Context) (int64, error) {
	v, err := s.meta(ctx, metaFloor)
	if err != nil || v == "" {
		return 0, err
	}
	h, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("floor %q: %w", v, err)
	}
	return h, nil
}

// SetFloor records the lowest height the source serves.
func (s *Store) SetFloor(ctx context.Context, height int64) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO blocks_meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		metaFloor, strconv.FormatInt(height, 10),
	); err != nil {
		return fmt.Errorf("write floor: %w", err)
	}
	return nil
}

// Reset drops the whole history and marks the store as holding chainID's.
func (s *Store) Reset(ctx context.Context, chainID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reset tx: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{
		"blocks", "blocks_valsets", "blocks_valset_members", "blocks_absences", "blocks_rounds",
		"blocks_flags", "blocks_events", "blocks_vote_delays", "blocks_meta",
	} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("reset %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO blocks_meta(key, value) VALUES(?, ?)`, metaChainID, chainID,
	); err != nil {
		return fmt.Errorf("reset chain id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset: %w", err)
	}
	return nil
}

func (s *Store) meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM blocks_meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", key, err)
	}
	return v, nil
}
