package blocks

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

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
		if ms < e || (ms < 0 && ms == e) {
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
	}, `SELECT d.day, v.address, d.votes, d.sum_ms, d.b0, d.b1, d.b2, d.b3, d.b4, d.b5, d.b6
	    FROM blocks_vote_delays d JOIN blocks_validators v ON v.id = d.validator
	    WHERE d.day BETWEEN ? AND ? ORDER BY d.day, v.address`, dayOf(from), dayOf(to))
	if err != nil {
		return nil, fmt.Errorf("vote delays: %w", err)
	}
	return out, nil
}

// secondsPerDay is a UTC day's length: vote delays are counted per day.
const secondsPerDay = 24 * 60 * 60

// dayOf is the UTC day of t, in days since the Unix epoch.
func dayOf(t time.Time) int64 { return t.UTC().Unix() / secondsPerDay }
