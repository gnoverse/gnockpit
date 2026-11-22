//go:build page

package web

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/gno/tm2/pkg/crypto"
	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
)

// ---- Validators

// specValset is the block history's validator set.
const specValset = "VALIDATORS"

// specHistory is the block history of the specs' last day, a block a
// minute up to the head: per proposal a validator made, a block it made at
// round 0; per proposal it missed, a block the standby made at round 1
// after it; then blocks the standby made. A validator misses the first
// blocks, as many as it is absent from.
func specHistory(specs []valSpec) []blocks.Record {
	var recs []blocks.Record
	absent := 1
	for _, s := range specs {
		for range s.proposals - s.missedProposals {
			recs = append(recs, blocks.Record{Proposer: s.val.address()})
		}
		for range s.missedProposals {
			recs = append(recs, blocks.Record{Proposer: standbyAddress, Round: 1, Rounds: []string{s.val.address(), standbyAddress}})
		}
		absent = max(absent, s.absent)
	}
	for len(recs) < absent {
		recs = append(recs, blocks.Record{Proposer: standbyAddress})
	}
	first := int64(pageHeight - len(recs) + 1)
	for i := range recs {
		h := first + int64(i)
		recs[i].Height, recs[i].Time, recs[i].ValsetHash = h, healthyBlockTime(h), specValset
		for _, s := range specs {
			if i < s.absent {
				recs[i].Absences = append(recs[i].Absences, blocks.Absence{Address: s.val.address(), Kind: blocks.Absent})
			} else {
				recs[i].Delays = append(recs[i].Delays, blocks.Delay{Address: s.val.address()})
			}
		}
	}
	return recs
}

// standbyAddress is a node outside the set: the proposer of the rounds a
// validator missed.
var standbyAddress = newPageValidator("standby", "standby", 0).address()

// ---- Set changes

// opBravo is bravo's operator in setChangesNetwork.
var opBravo = preimageAddress("operator bravo")

// preimageAddress is a g1 address derived from preimage.
func preimageAddress(preimage string) string {
	return crypto.AddressFromPreimage([]byte(preimage)).String()
}

// setChangesNetwork is the healthy network with a history of set changes, its
// powers in units of unit:
//   - alpha, charlie and echo are the genesis set, at 10 each;
//   - delta joins at 1001 at 5, an event linking it to its operator;
//   - bravo joins at 2001 at 8;
//   - at 3001 bravo goes to 10 and echo leaves.
//
// Bravo and charlie have valopers profiles; alpha, delta and echo none.
// Bravo's peer is in Germany at Hetzner, charlie's at OVH in no known
// country, delta's in the US at no known provider.
func setChangesNetwork(t *testing.T, unit int64) *scenario {
	t.Helper()
	sc := healthyNetwork()
	sc.names[valEcho.address()] = valEcho.moniker
	for i := range sc.snap.Validators {
		v := &sc.snap.Validators[i]
		power, err := strconv.ParseInt(v.VotingPower, 10, 64)
		if err != nil {
			t.Fatalf("power of %s: %v", v.Address, err)
		}
		v.VotingPower = strconv.FormatInt(power*unit, 10)
	}
	sc.snap.Peers = historyPeers(t)
	sc.seed = setChangesHistory(1, unit)
	sc.valopers = map[string]node.Valoper{
		opBravo: {
			Operator: opBravo, Moniker: valBravo.moniker, Description: "Runs **bravo** on bare metal.",
			ServerType: "bare metal", SigningAddress: valBravo.address(), SigningPubKey: "gpub1bravo",
			State: &node.ValoperState{KeepRunning: true, LastRotationHeight: bravoJoins},
		},
		opCharlie: {Operator: opCharlie, Moniker: valCharlie.moniker, SigningAddress: valCharlie.address(), SigningPubKey: "gpub1charlie"},
	}
	return sc
}

// historyPeers are the healthy network's peers, bravo's and charlie's
// located, and a peer of delta's in the US, sorted by moniker. Delta's is a
// copy of charlie's: the test fails when the healthy peers lose charlie.
func historyPeers(t *testing.T) []node.Peer {
	t.Helper()
	var out []node.Peer
	var charlie node.Peer
	found := false
	for _, p := range healthyPeers() {
		switch p.Moniker {
		case "bravo":
			p.Country, p.City, p.Provider = "DE", "Berlin", "Hetzner"
		case "charlie":
			p.Provider = "OVH"
			charlie, found = p, true
		}
		out = append(out, p)
	}
	if !found {
		t.Fatal("historyPeers: the healthy network has no peer named charlie to copy for delta's")
	}
	delta := charlie
	delta.Moniker, delta.RemoteIP, delta.NodeID = "delta", "203.0.113.15", nodeID("delta")
	delta.P2PAddress = delta.NodeID + "@" + delta.RemoteIP + ":26656"
	delta.RPCURL = "http://" + delta.RemoteIP + ":26657"
	delta.ValAddress, delta.ValPubKey = valDelta.address(), valDelta.pubKey().Value
	delta.Country, delta.Provider = "US", ""
	out = append(out, delta)
	slices.SortFunc(out, func(a, b node.Peer) int { return strings.Compare(a.Moniker, b.Moniker) })
	return out
}

var valEcho = newPageValidator("echo", "echo", 10)

var opCharlie = preimageAddress("operator charlie")

const bravoJoins = 2001

// setChangesHistory writes setChangesNetwork's block history from height from,
// one block a minute as the healthy network's, powers in units of unit.
func setChangesHistory(from, unit int64) func(t *testing.T, s *blocks.Store) {
	member := func(v pageValidator, power int64) blocks.Member { return memberAt(v, power*unit) }
	alpha, charlie := member(valAlpha, 10), member(valCharlie, 10)
	sets := []struct {
		hash     string
		first    int64
		last     int64
		members  []blocks.Member
		joinedBy *blocks.Event // the event of the change at first, if any
	}{
		{"GENESIS", 1, deltaJoins - 1, []blocks.Member{alpha, charlie, member(valEcho, 10)}, nil},
		{"DELTA", deltaJoins, bravoJoins - 1, []blocks.Member{alpha, charlie, member(valEcho, 10), member(valDelta, 5)}, &blocks.Event{
			Type: "ValidatorAdded", Operator: opDelta, Address: valDelta.address(), Power: 5 * unit, Applied: true,
		}},
		{"BRAVO", bravoJoins, bravoRepowered - 1, []blocks.Member{alpha, charlie, member(valEcho, 10), member(valDelta, 5), member(valBravo, 8)}, nil},
		{"REPOWERED", bravoRepowered, pageHeight, []blocks.Member{alpha, member(valBravo, 10), charlie, member(valDelta, 5)}, nil},
	}
	return func(t *testing.T, s *blocks.Store) {
		t.Helper()
		for _, set := range sets {
			if set.last < from {
				continue
			}
			putBlocks(t, s, healthyBlockTime, set.hash, set.members, max(set.first, from), set.last, healthyAbsences())
			if set.joinedBy != nil && set.first >= from {
				putValEvents(t, s, set.first, *set.joinedBy)
			}
		}
	}
}

var opDelta = preimageAddress("operator delta")

// The heights of the valpage network's set changes.
const (
	deltaJoins = 1001

	bravoRepowered = 3001 // echo leaves at the same height
)

// ---- Chart chains

// historyCadence is the time between two blocks of a chart scenario's
// session: as long as it gets without a restart.
const historyCadence = blocks.RestartGap

// utc2026 is a time of 2026, in UTC.
func utc2026(month time.Month, day, hour, minute int) time.Time {
	return time.Date(2026, month, day, hour, minute, 0, 0, time.UTC)
}

// historyChain is a block history a chart scenario writes: sessions of
// blocks historyCadence apart from height 1 on, the gap between two
// sessions a restart. Every member votes on time and the first member
// proposes at round 0, unless a block says otherwise.
type historyChain struct {
	t       *testing.T
	members []blocks.Member
	blocks  []chainBlock // ascending: block i is height i+1
}

// chainBlock is one block of a historyChain.
type chainBlock struct {
	at       time.Time
	proposer string // "": the chain's first member
	// missedBy is the expected proposer of round 0 when the block was made
	// at round 1.
	missedBy string
	votes    map[string]blocks.Kind // a member's precommit that is not for the block
	delayMs  map[string]int64       // a member's precommit's delay; 0 when unset
}

func newHistoryChain(t *testing.T, vals ...pageValidator) *historyChain {
	return &historyChain{t: t, members: members(vals...)}
}

// session appends the blocks from from to to, historyCadence apart.
func (c *historyChain) session(from, to time.Time) *historyChain {
	c.t.Helper()
	return c.every(historyCadence, from, to)
}

// every appends the blocks from from to to, step apart.
func (c *historyChain) every(step time.Duration, from, to time.Time) *historyChain {
	c.t.Helper()
	for at := from; !at.After(to); at = at.Add(step) {
		c.at(at)
	}
	return c
}

// at appends a block at each of times.
func (c *historyChain) at(times ...time.Time) *historyChain {
	c.t.Helper()
	for _, at := range times {
		if n := len(c.blocks); n > 0 && !at.After(c.blocks[n-1].at) {
			c.t.Fatalf("block at %s: not after the chain's last block, %s", at, c.blocks[n-1].at)
		}
		c.blocks = append(c.blocks, chainBlock{at: at, votes: map[string]blocks.Kind{}, delayMs: map[string]int64{}})
	}
	return c
}

// each calls fn on the blocks from from to to, both included.
func (c *historyChain) each(from, to time.Time, fn func(i int, b *chainBlock)) *historyChain {
	n := 0
	for i := range c.blocks {
		if b := &c.blocks[i]; !b.at.Before(from) && !b.at.After(to) {
			fn(n, b)
			n++
		}
	}
	return c
}

// vote marks v's precommits of the blocks from from to to as kind: absent,
// nil or for another block.
func (c *historyChain) vote(v pageValidator, kind blocks.Kind, from, to time.Time) *historyChain {
	return c.each(from, to, func(_ int, b *chainBlock) { b.votes[v.address()] = kind })
}

// absent marks v absent from the blocks from from to to.
func (c *historyChain) absent(v pageValidator, from, to time.Time) *historyChain {
	return c.vote(v, blocks.Absent, from, to)
}

// delay sets v's precommit delay in the blocks from from to to to ms.
func (c *historyChain) delay(v pageValidator, ms int64, from, to time.Time) *historyChain {
	return c.each(from, to, func(_ int, b *chainBlock) { b.delayMs[v.address()] = ms })
}

// propose makes v the proposer of the blocks from from to to, at round 0.
func (c *historyChain) propose(v pageValidator, from, to time.Time) *historyChain {
	return c.each(from, to, func(_ int, b *chainBlock) { b.proposer = v.address() })
}

// missProposals makes the blocks from from to to ones made at round 1: v
// was the expected proposer of round 0 and missed it.
func (c *historyChain) missProposals(v pageValidator, from, to time.Time) *historyChain {
	return c.each(from, to, func(_ int, b *chainBlock) { b.missedBy = v.address() })
}

// write stores the chain on the validator set hash.
func (c *historyChain) write(t *testing.T, s *blocks.Store, hash string) {
	t.Helper()
	ctx := context.Background()
	if err := s.PutValset(ctx, hash, c.members); err != nil {
		t.Fatal(err)
	}
	recs := make([]blocks.Record, len(c.blocks))
	for i, b := range c.blocks {
		r := blocks.Record{Height: int64(i + 1), Time: b.at, Proposer: cmp.Or(b.proposer, c.members[0].Address), ValsetHash: hash}
		if b.missedBy != "" {
			r.Round, r.Rounds = 1, []string{b.missedBy, r.Proposer}
		}
		for _, m := range c.members {
			kind, failed := b.votes[m.Address]
			if failed {
				r.Absences = append(r.Absences, blocks.Absence{Address: m.Address, Kind: kind})
			}
			// A nil precommit or one for another block is a vote, with
			// its delay, as a block's record has it.
			if kind != blocks.Absent {
				r.Delays = append(r.Delays, blocks.Delay{Address: m.Address, Ms: b.delayMs[m.Address]})
			}
		}
		recs[i] = r
	}
	if err := s.Write(ctx, recs); err != nil {
		t.Fatal(err)
	}
}

// ---- Chart networks

// monthNetwork is the healthy network on a month of block history, one
// block every historyCadence from 2026-10-20, alpha, bravo and charlie in
// the set at 10 each; delta has none. The chain restarts five times: no
// block on 2026-10-24, from 2026-11-03 12:00 to 2026-11-04 00:02, around
// 06:00 and 18:00 on 2026-11-10, and from 2026-11-18 10:00 to 11:00.
//
// Bravo, whose page the chart tests open:
//   - 2026-10-20: every precommit, delays from −2 s to +6 s;
//   - 2026-10-21: absent from 22 blocks, 2 nil precommits and 1 for
//     another block, every vote 0.3 s early;
//   - two outages: 60 blocks from 2026-10-22 23:00, 15 from 2026-10-23
//     10:00;
//   - signs again 10 min, 24 h, 2 min and 12 min after the first four
//     restarts, and not after the fifth;
//   - proposes 40 blocks on 2026-10-26, 40 on 2026-10-27 missing 1, 20 on
//     2026-10-28 missing 1.
//
// Charlie is absent 100 blocks from 06:00 on ten days, and proposes 10
// blocks from 2026-11-17 20:00, missing 1. Alpha proposes the rest and
// signs every block.
func monthNetwork(t *testing.T) *scenario {
	t.Helper()
	sc := healthyNetwork()
	// Delta has no precommit in the live window.
	delete(sc.snap.Signing.ValidatorSigning, valDelta.address())
	c := monthHistory(t)
	sc.seed = func(t *testing.T, s *blocks.Store) { c.write(t, s, "CHARTS") }
	sc.fill = blocks.Progress{ChainID: pageChainID, From: 1, To: int64(len(c.blocks)), FromTime: c.blocks[0].at, Complete: true}
	return sc
}

// ongoingOutageNetwork is the healthy network on three days of block history to
// a head at 2026-11-18 00:00:00.000, bravo absent from the 31 blocks since
// 2026-11-17 23:00: an outage still going on.
func ongoingOutageNetwork(t *testing.T) *scenario {
	t.Helper()
	c := newHistoryChain(t, valAlpha, valBravo, valCharlie).
		session(utc2026(time.November, 15, 0, 0), ongoingHead).
		absent(valBravo, utc2026(time.November, 17, 23, 0), ongoingHead)
	sc := healthyNetwork()
	sc.seed = func(t *testing.T, s *blocks.Store) { c.write(t, s, "ONGOING") }
	sc.fill = blocks.Progress{ChainID: pageChainID, From: 1, To: int64(len(c.blocks)), FromTime: c.blocks[0].at, Complete: true}
	return sc
}

// Foxtrot is an operator whose signing key went through every set change.
var (
	opFoxtrot  = preimageAddress("operator foxtrot")
	foxtrotOld = newPageValidator("foxtrot old", "foxtrot", 10)
	foxtrotNew = newPageValidator("foxtrot new", "foxtrot", 5)
)

// foxtrotNetwork is the healthy network on the healthy history of
// alpha, bravo and charlie at 10 each, beside foxtrot's keys: its first in
// the genesis set at 10, at 20 from 1000, out of the set from 2000, back at
// 5 from 2500, then rotated to its second at 3000.
func foxtrotNetwork(t *testing.T) *scenario {
	t.Helper()
	sc := healthyNetwork()
	sc.names[foxtrotNew.address()] = foxtrotNew.moniker
	base := []blocks.Member{memberAt(valAlpha, 10), memberAt(valBravo, 10), memberAt(valCharlie, 10)}
	sets := []struct {
		hash        string
		first, last int64
		foxtrot     []blocks.Member
	}{
		{"F-GENESIS", 1, 999, []blocks.Member{memberAt(foxtrotOld, 10)}},
		{"F-POWER", 1000, 1999, []blocks.Member{memberAt(foxtrotOld, 20)}},
		{"F-LEFT", 2000, 2499, nil},
		{"F-JOINED", 2500, 2999, []blocks.Member{memberAt(foxtrotOld, 5)}},
		{"F-ROTATED", 3000, pageHeight, []blocks.Member{memberAt(foxtrotNew, 5)}},
	}
	sc.seed = func(t *testing.T, s *blocks.Store) {
		for _, set := range sets {
			putBlocks(t, s, healthyBlockTime, set.hash, append(slices.Clone(base), set.foxtrot...), set.first, set.last, nil)
		}
		putValEvents(t, s, 3000, blocks.Event{
			Type: "ValoperRotated", Operator: opFoxtrot, OldAddress: foxtrotOld.address(),
			NewAddress: foxtrotNew.address(), Power: 5, Applied: true,
		})
	}
	return sc
}

// timedOutagesNetwork is the healthy network on a block history of alpha and
// bravo at 10 each where bravo has an outage of each of outageDurations,
// from 2026-11-01, an hour apart: 11 blocks a second apart, then the block
// that ends it its length after the first. Its gaps hold no recovery: the
// outage had begun ten blocks before.
func timedOutagesNetwork(t *testing.T) *scenario {
	t.Helper()
	c := newHistoryChain(t, valAlpha, valBravo)
	first := utc2026(time.November, 1, 0, 0)
	for _, d := range outageDurations {
		for i := range 11 {
			c.at(first.Add(time.Duration(i) * time.Second))
		}
		c.absent(valBravo, first, first.Add(10*time.Second)).at(first.Add(d))
		first = first.Add(d + time.Hour)
	}
	sc := healthyNetwork()
	sc.seed = func(t *testing.T, s *blocks.Store) { c.write(t, s, "TIMED") }
	sc.fill = blocks.Progress{ChainID: pageChainID, From: 1, To: int64(len(c.blocks)), FromTime: c.blocks[0].at, Complete: true}
	return sc
}

// monthHistory is monthNetwork's block history.
func monthHistory(t *testing.T) *historyChain {
	t.Helper()
	c := newHistoryChain(t, valAlpha, valBravo, valCharlie).
		session(utc2026(time.October, 20, 0, 0), monthRestart1).
		session(utc2026(time.October, 25, 12, 0), monthRestart2).
		session(utc2026(time.November, 4, 0, 2), monthRestart3).
		session(utc2026(time.November, 10, 7, 0), monthRestart4).
		session(utc2026(time.November, 10, 19, 0), monthRestart5).
		session(utc2026(time.November, 18, 11, 0), utc2026(time.November, 18, 11, 58))

	// Bravo's 2026-10-20: 1 vote under −1 s, 180 at −0.5 s, 360 on time,
	// 108 at +0.6 s, 36 at +1.5 s, 18 at +3 s and 17 at +6 s.
	delays := []struct {
		votes int
		ms    int64
	}{{1, -2000}, {180, -500}, {360, 0}, {108, 600}, {36, 1500}, {18, 3000}, {17, 6000}}
	c.each(utc2026(time.October, 20, 0, 0), utc2026(time.October, 20, 23, 58), func(i int, b *chainBlock) {
		for _, d := range delays {
			if i < d.votes {
				b.delayMs[valBravo.address()] = d.ms
				return
			}
			i -= d.votes
		}
	})
	for h := range 22 {
		at := utc2026(time.October, 21, h, 30)
		c.absent(valBravo, at, at)
	}
	c.vote(valBravo, blocks.Nil, utc2026(time.October, 21, 23, 10), utc2026(time.October, 21, 23, 10)).
		vote(valBravo, blocks.Nil, utc2026(time.October, 21, 23, 20), utc2026(time.October, 21, 23, 20)).
		vote(valBravo, blocks.OtherBlock, utc2026(time.October, 21, 23, 30), utc2026(time.October, 21, 23, 30)).
		delay(valBravo, -300, utc2026(time.October, 21, 0, 0), utc2026(time.October, 21, 23, 58)).
		absent(valBravo, utc2026(time.October, 22, 23, 0), utc2026(time.October, 23, 0, 58)).
		absent(valBravo, utc2026(time.October, 23, 10, 0), utc2026(time.October, 23, 10, 28)).
		// Restarts: absent from the block before each until it signs again.
		absent(valBravo, monthRestart1, utc2026(time.October, 25, 12, 6)).
		absent(valBravo, monthRestart2, utc2026(time.November, 4, 23, 58)).
		absent(valBravo, monthRestart3, monthRestart3).
		absent(valBravo, monthRestart4, utc2026(time.November, 10, 19, 8)).
		absent(valBravo, monthRestart5, utc2026(time.November, 18, 11, 58)).
		propose(valBravo, utc2026(time.October, 26, 12, 0), utc2026(time.October, 26, 13, 18)).
		propose(valBravo, utc2026(time.October, 27, 12, 0), utc2026(time.October, 27, 13, 16)).
		missProposals(valBravo, utc2026(time.October, 27, 13, 18), utc2026(time.October, 27, 13, 18)).
		propose(valBravo, utc2026(time.October, 28, 12, 0), utc2026(time.October, 28, 12, 36)).
		missProposals(valBravo, utc2026(time.October, 28, 12, 38), utc2026(time.October, 28, 12, 38))

	for _, day := range []time.Time{
		utc2026(time.October, 29, 6, 0), utc2026(time.October, 30, 6, 0), utc2026(time.October, 31, 6, 0),
		utc2026(time.November, 1, 6, 0), utc2026(time.November, 2, 6, 0), utc2026(time.November, 11, 6, 0),
		utc2026(time.November, 12, 6, 0), utc2026(time.November, 13, 6, 0), utc2026(time.November, 14, 6, 0),
		utc2026(time.November, 15, 6, 0),
	} {
		c.absent(valCharlie, day, day.Add(99*historyCadence))
	}
	c.propose(valCharlie, utc2026(time.November, 17, 20, 0), utc2026(time.November, 17, 20, 16)).
		missProposals(valCharlie, utc2026(time.November, 17, 20, 18), utc2026(time.November, 17, 20, 18))
	return c
}

// ongoingHead is the head of ongoingOutageNetwork's chain.
var ongoingHead = utc2026(time.November, 18, 0, 0)

// outageDurations are the lengths of timedOutagesNetwork's outages, oldest
// first.
var outageDurations = []time.Duration{
	59 * time.Second, 59500 * time.Millisecond, 3599 * time.Second, time.Hour,
	47*time.Hour + 59*time.Minute, 48 * time.Hour, 60 * time.Hour,
}

// The restarts of monthNetwork's chain, by the time of the last block
// before each.
var (
	monthRestart1 = utc2026(time.October, 23, 23, 58)
	monthRestart2 = utc2026(time.November, 3, 11, 58)
	monthRestart3 = utc2026(time.November, 10, 5, 58)
	monthRestart4 = utc2026(time.November, 10, 17, 58)
	monthRestart5 = utc2026(time.November, 18, 9, 58)
)
