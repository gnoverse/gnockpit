//go:build page

package web

import (
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
)

// rankingNewcomer is a validator that joins the ranking chain's set too
// late to be ranked.
var rankingNewcomer = newPageValidator("newcomer", "newcomer", 5)

// opRankingEcho is echo's operator in rankingNetwork: its valopers profile
// names echo's key.
var opRankingEcho = preimageAddress("operator echo")

// The ranking chain's times: two sessions, blocks historyCadence apart, the
// chain restarting between them.
var (
	rankingChainStart = utc2026(time.November, 9, 0, 0)
	// rankingRestart is block b's: the last block before the restart.
	rankingRestart = utc2026(time.November, 9, 12, 0)
	// rankingResume is block b+1's: the first block after it.
	rankingResume = utc2026(time.November, 9, 13, 0)
	// rankingChainHead is the last block's, 2 min before pageNow.
	rankingChainHead = utc2026(time.November, 18, 11, 58)
	// rankingDeltaJoins and rankingNewcomerJoins are the first blocks the
	// sets holding delta and the newcomer sign.
	rankingDeltaJoins    = utc2026(time.November, 10, 0, 0)
	rankingNewcomerJoins = utc2026(time.November, 14, 0, 30)
)

// rankingNetwork is the healthy network on nine and a half days of block
// history, ranked as of pageNow (the window: 2026-10-19 to 2026-11-17, its
// blocks from height 1, 2026-11-09 00:00, on). Echo proposes every block
// but those named below, and signs every block. The chain restarts after
// 2026-11-09 12:00, the next block coming at 13:00.
//
//   - alpha and charlie are equal on every measure, a shared first rank:
//     each absent from the four blocks of 2026-11-11 06:00, 06:10, 06:20
//     and 06:30, signing again at once after the restart; alpha proposes
//     the 60 blocks from 2026-11-15 10:00, charlie the 50 from 14:00;
//   - bravo, linked to opBravo by its profile, signs again 30 min after the
//     restart (absent from block b to 13:26) and proposes none;
//   - delta, named by its address, joins at 2026-11-10 00:00, after the
//     restart; it is absent from ten runs of 10 blocks, from 01:00 to 10:00
//     on 2026-11-12, and proposes the 99 blocks from 2026-11-16 10:00,
//     missing the next one (made at round 1);
//   - echo, linked to opRankingEcho by its profile, signs every block: the
//     ranking excludes its operator;
//   - the newcomer joins at 2026-11-14 00:30: 95.5 h in the set.
func rankingNetwork(t *testing.T) *scenario {
	t.Helper()
	sc := healthyNetwork()
	sc.names[valEcho.address()] = valEcho.moniker
	sc.names[rankingNewcomer.address()] = rankingNewcomer.moniker
	sc.valopers = map[string]node.Valoper{
		opBravo:       {Operator: opBravo, Moniker: valBravo.moniker, SigningAddress: valBravo.address()},
		opRankingEcho: {Operator: opRankingEcho, Moniker: valEcho.moniker, SigningAddress: valEcho.address()},
	}
	chain := rankingNetworkChain()
	chain.store(sc, chain.records())
	sc.ranking = &pageRanking{exclude: []string{opRankingEcho}}
	return sc
}

// rankingWindowSpan is the window of a ranking as of pageNow, as the
// popovers' titles and the explainer write its days.
const rankingWindowSpan = "Oct 19 – Nov 17 UTC"

// rankingWindowDays is the window of a ranking as of pageNow, as the meta
// line writes its days.
const rankingWindowDays = "30 days, " + rankingWindowSpan

// rankingNetworkWindow is the window of rankingNetwork's ranking, as the
// meta line writes it.
const rankingNetworkWindow = rankingWindowDays + " · heights 1 – 6,451"

// rankingSets are the ranking chain's validator sets, by hash: the genesis
// set, then delta, then the newcomer joining it.
var rankingSets = map[string][]pageValidator{
	"RANKING-GENESIS":  {valAlpha, valBravo, valCharlie, valEcho},
	"RANKING-DELTA":    {valAlpha, valBravo, valCharlie, valEcho, valDelta},
	"RANKING-NEWCOMER": {valAlpha, valBravo, valCharlie, valEcho, valDelta, rankingNewcomer},
}

// rankingSetAt is the hash of the set that signs the block at.
func rankingSetAt(at time.Time) string {
	switch {
	case !at.Before(rankingNewcomerJoins):
		return "RANKING-NEWCOMER"
	case !at.Before(rankingDeltaJoins):
		return "RANKING-DELTA"
	}
	return "RANKING-GENESIS"
}

// rankingChain is a ranking chain's block history, as a scenario stores it.
type rankingChain struct {
	// sessions are the chain's runs of blocks, historyCadence apart; it
	// restarts between two, block b the last of one, b+1 the first of the
	// next.
	sessions [][2]time.Time
	// sets are the chain's validator sets, by hash; setAt is the hash of
	// the set that signs the block at a time.
	sets  map[string][]pageValidator
	setAt func(time.Time) string
	// absent is who is absent from the block at each time.
	absent map[time.Time]map[string]bool
	// proposers are the round-0 proposers other than echo, by block time.
	proposers map[time.Time]pageValidator
	// misses are the blocks made at round 1, by block time: round 0's
	// proposer, who missed it, and round 1's, who made the block.
	misses map[time.Time][2]pageValidator
}

// records are the chain's blocks, from height 1.
func (c rankingChain) records() []blocks.Record {
	var recs []blocks.Record
	for _, session := range c.sessions {
		for at := session[0]; !at.After(session[1]); at = at.Add(historyCadence) {
			hash := c.setAt(at)
			r := blocks.Record{Height: int64(len(recs) + 1), Time: at, Proposer: valEcho.address(), ValsetHash: hash}
			if v, ok := c.proposers[at]; ok {
				r.Proposer = v.address()
			}
			if m, ok := c.misses[at]; ok {
				r.Proposer, r.Round, r.Rounds = m[1].address(), 1, []string{m[0].address(), m[1].address()}
			}
			for _, v := range c.sets[hash] {
				if c.absent[at][v.address()] {
					r.Absences = append(r.Absences, blocks.Absence{Address: v.address(), Kind: blocks.Absent})
					continue
				}
				r.Delays = append(r.Delays, blocks.Delay{Address: v.address()})
			}
			recs = append(recs, r)
		}
	}
	return recs
}

// store makes recs, the chain's blocks, sc's block history, filled from
// the first to the last, with the chain's validator sets.
func (c rankingChain) store(sc *scenario, recs []blocks.Record) {
	sc.seed = func(t *testing.T, s *blocks.Store) {
		t.Helper()
		for hash, vals := range c.sets {
			if err := s.PutValset(t.Context(), hash, members(vals...)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Write(t.Context(), recs); err != nil {
			t.Fatal(err)
		}
	}
	sc.fill = blocks.Progress{ChainID: pageChainID, From: 1, To: int64(len(recs)), FromTime: recs[0].Time, Complete: true}
}

// rankingAbsent marks v absent from the blocks at from to to.
func rankingAbsent(absent map[time.Time]map[string]bool, v pageValidator, from, to time.Time) {
	for at := from; !at.After(to); at = at.Add(historyCadence) {
		if absent[at] == nil {
			absent[at] = map[string]bool{}
		}
		absent[at][v.address()] = true
	}
}

// rankingProposing makes v round 0's proposer of the count blocks from
// from.
func rankingProposing(proposers map[time.Time]pageValidator, v pageValidator, from time.Time, count int) {
	for i := range count {
		proposers[from.Add(time.Duration(i)*historyCadence)] = v
	}
}

// rankingNetworkChain is rankingNetwork's chain.
func rankingNetworkChain() rankingChain {
	absent := map[time.Time]map[string]bool{}
	for _, minute := range []int{0, 10, 20, 30} {
		at := utc2026(time.November, 11, 6, minute)
		rankingAbsent(absent, valAlpha, at, at)
		rankingAbsent(absent, valCharlie, at, at)
	}
	rankingAbsent(absent, valBravo, rankingRestart, rankingRestart)
	rankingAbsent(absent, valBravo, rankingResume, rankingResume.Add(26*time.Minute))
	for hour := 1; hour <= 10; hour++ {
		from := utc2026(time.November, 12, hour, 0)
		rankingAbsent(absent, valDelta, from, from.Add(9*historyCadence))
	}
	proposers := map[time.Time]pageValidator{}
	rankingProposing(proposers, valAlpha, utc2026(time.November, 15, 10, 0), 60)
	rankingProposing(proposers, valCharlie, utc2026(time.November, 15, 14, 0), 50)
	rankingProposing(proposers, valDelta, utc2026(time.November, 16, 10, 0), 99)
	return rankingChain{
		sessions:  [][2]time.Time{{rankingChainStart, rankingRestart}, {rankingResume, rankingChainHead}},
		sets:      rankingSets,
		setAt:     rankingSetAt,
		absent:    absent,
		proposers: proposers,
		misses:    map[time.Time][2]pageValidator{utc2026(time.November, 16, 13, 18): {valDelta, valEcho}},
	}
}

// rankingProposalsDisabled is rankingNetwork with one block of its window,
// height 100, flagged when stored: its proposals cannot be checked, which
// disables the proposals sub-score for every operator.
func rankingProposalsDisabled(t *testing.T) *scenario {
	t.Helper()
	sc := rankingNetwork(t)
	chain := rankingNetworkChain()
	recs := chain.records()
	recs[99].Flag = "rounds unknown"
	chain.store(sc, recs)
	return sc
}

// rankingEchoMisses is rankingNetwork with echo missing one proposal of
// the 6,241 it is due, at 2026-11-17 06:00, alpha making that block at
// round 1: echo's proposals sub-score and score fall short of full marks
// by less than the half-tenth a rounding up would hide.
func rankingEchoMisses(t *testing.T) *scenario {
	t.Helper()
	sc := rankingNetwork(t)
	chain := rankingNetworkChain()
	chain.misses[utc2026(time.November, 17, 6, 0)] = [2]pageValidator{valEcho, valAlpha}
	chain.store(sc, chain.records())
	return sc
}

// rankingRestarts are the restarts chain's block b times, 12:00 on
// 2026-11-10, -12 and -14: the next block comes an hour later.
var rankingRestarts = []time.Time{
	utc2026(time.November, 10, 12, 0),
	utc2026(time.November, 12, 12, 0),
	utc2026(time.November, 14, 12, 0),
}

// rankingRestartsNetwork is the healthy network on nine and a half days of
// block history, from height 1, 2026-11-09 00:00, its one set of alpha,
// bravo, charlie, delta and echo signing every block but those named
// below, ranked as of pageNow, nothing excluded. The chain restarts at
// each of rankingRestarts. Echo proposes every block but delta's.
//
//   - alpha, charlie and echo, signing every block, share the first rank:
//     equal on the score and every tie-break;
//   - bravo signs again 10, 30, then 20 min after the restarts, absent from
//     each block b and from b+1 on to 4 min short of that: three recovery
//     samples, a median of 20 min;
//   - delta, named by its address, proposes the 149 blocks from 2026-11-16
//     10:00 and misses the next one (made at round 1): 1 missed of 150.
func rankingRestartsNetwork() *scenario {
	sc := healthyNetwork()
	sc.names[valEcho.address()] = valEcho.moniker
	absent := map[time.Time]map[string]bool{}
	var sessions [][2]time.Time
	from := rankingChainStart
	for i, b := range rankingRestarts {
		sessions = append(sessions, [2]time.Time{from, b})
		from = b.Add(time.Hour)
		back := []time.Duration{10, 30, 20}[i] * time.Minute
		rankingAbsent(absent, valBravo, b, b)
		rankingAbsent(absent, valBravo, from, from.Add(back-4*time.Minute))
	}
	sessions = append(sessions, [2]time.Time{from, rankingChainHead})
	proposers := map[time.Time]pageValidator{}
	deltaFrom := utc2026(time.November, 16, 10, 0)
	rankingProposing(proposers, valDelta, deltaFrom, 149)
	chain := rankingChain{
		sessions:  sessions,
		sets:      map[string][]pageValidator{"RANKING-RESTARTS": {valAlpha, valBravo, valCharlie, valDelta, valEcho}},
		setAt:     func(time.Time) string { return "RANKING-RESTARTS" },
		absent:    absent,
		proposers: proposers,
		misses:    map[time.Time][2]pageValidator{deltaFrom.Add(149 * historyCadence): {valDelta, valEcho}},
	}
	chain.store(sc, chain.records())
	sc.ranking = &pageRanking{}
	return sc
}

// rankingHealthy is the healthy network with the ranking on: its three days
// of history leave every operator short of 168 h in the set, not ranked yet.
func rankingHealthy() *scenario {
	sc := healthyNetwork()
	sc.ranking = &pageRanking{}
	return sc
}

// rankingFillingProgress is the fill progress of a history holding the
// healthy network's heights from 1,000 on, 3,321 of its 4,320, filling at
// rate heights a second: 0 while unmeasured.
func rankingFillingProgress(rate float64) blocks.Progress {
	return blocks.Progress{ChainID: pageChainID, From: 1000, To: pageHeight, FromTime: healthyBlockTime(1000), Rate: rate}
}

// rankingFromHeight is the healthy network with its block history stored
// from height 1,000 on, filled as p says, and the ranking on.
func rankingFromHeight(p blocks.Progress) *scenario {
	sc := healthyNetwork()
	set := members(healthyValidators()...)
	sc.seed = func(t *testing.T, s *blocks.Store) {
		putBlocks(t, s, healthyBlockTime, "HEALTHY", set, 1000, pageHeight, healthyAbsences())
	}
	sc.fill = p
	sc.ranking = &pageRanking{}
	return sc
}

// rankingTodayOnly is the healthy network on a history of today's blocks
// alone, its last 720 from 00:00:58 on, renumbered from height 1, and the
// ranking on: its window, the days before today, holds no block.
func rankingTodayOnly() *scenario {
	sc := healthyNetwork()
	set := members(healthyValidators()...)
	at := func(h int64) time.Time { return healthyBlockTime(h + pageHeight - 720) }
	sc.seed = func(t *testing.T, s *blocks.Store) { putBlocks(t, s, at, "HEALTHY", set, 1, 720, nil) }
	sc.fill = blocks.Progress{ChainID: pageChainID, From: 1, To: 720, FromTime: at(1), Complete: true}
	sc.ranking = &pageRanking{}
	return sc
}
