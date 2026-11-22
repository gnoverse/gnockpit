//go:build page

package web

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
)

const (
	// liveReconnect is how long the page waits to connect again once its
	// WebSocket is lost.
	liveReconnect = 2 * time.Second
	// liveFrame is a little more than an animation frame of the browser's
	// installed clock.
	liveFrame = 20 * time.Millisecond
	// liveBanner is the banner of a lost WebSocket.
	liveBanner = "#error-banner"
)

// ---- Builders

// liveRound is the healthy network in round round, started age before
// pageNow.
func liveRound(round int, age time.Duration) *node.Snapshot {
	snap := healthySnapshot()
	start := pageNow.Add(-age).Format(time.RFC3339Nano)
	snap.Consensus.Round = strconv.Itoa(round)
	snap.Consensus.RoundStartTime = start
	snap.RoundStartTime = start
	return snap
}

// liveHeadAt is the healthy network whose head block was made age before
// pageNow.
func liveHeadAt(age time.Duration) *node.Snapshot {
	snap := healthySnapshot()
	snap.Status.SyncInfo.LatestBlockTime = pageNow.Add(-age).Format(time.RFC3339Nano)
	return snap
}

// liveGenesisIn is the healthy network with its genesis at pageNow + in:
// in the past for a negative in.
func liveGenesisIn(in time.Duration) *node.Snapshot {
	snap := healthySnapshot()
	snap.System.GenesisTime = pageNow.Add(in).Format(nodeTimeLayout)
	return snap
}

// liveBeforeGenesis is a chain whose genesis is in away: its node is up,
// with no block yet.
func liveBeforeGenesis(in time.Duration) *scenario {
	return &scenario{now: pageNow, snap: &node.Snapshot{
		Status: &node.Status{
			NodeInfo: node.NodeInfo{Moniker: valAlpha.moniker, Network: pageChainID},
			SyncInfo: node.SyncInfo{LatestBlockHeight: "0", LatestBlockTime: "1970-01-01T00:00:00Z"},
		},
		System:    &node.SystemInfo{NodeTime: pageNow.Format(nodeTimeLayout), GenesisTime: pageNow.Add(in).Format(nodeTimeLayout)},
		Timestamp: pageNow,
	}}
}

// ---- Locators

// liveText reads the text of the element selector matches at once, without
// waiting: it checks the page before a frame the test holds back.
func liveText(e *pageEnv, selector string) string {
	e.T.Helper()
	s, err := e.Page.Locator(selector).TextContent()
	e.must(err)
	return s
}

// liveAwaitFrame waits until the browser has drawn a frame: the frames the
// page asked for before it have run.
func liveAwaitFrame(e *pageEnv) {
	e.T.Helper()
	// An intersection observer reports after the next rendering of the
	// page, whose animation frames run first.
	e.must(e.expect("body").ToBeInViewport())
}

// ---- WebSocket

// TestPageWebSocketReconnects checks the live connection: the page opens it
// on load and shows the server's snapshot; each loss shows the banner, and
// 2 s later the page connects again, shows the server's latest snapshot and
// hides the banner.
func TestPageWebSocketReconnects(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork(), withInstalledClock())
		banner := e.expect(liveBanner)
		e.must(e.expect("#d-block").ToHaveText(strconv.Itoa(pageHeight)))
		e.must(banner.ToBeHidden())

		for i := 1; i <= 2; i++ {
			e.closeWebSockets()
			e.must(banner.ToBeVisible())
			e.must(banner.ToHaveText("WebSocket connection lost, reconnecting..."))
			// Published while the page is away: the snapshot it gets on
			// connecting.
			e.publish(healthyAt(pageHeight + i))
			e.runFor(liveReconnect)
			e.must(e.expect("#d-block").ToHaveText(strconv.Itoa(pageHeight + i)))
			e.must(banner.ToBeHidden())
		}
	})
}

// TestPageLiveUpdates checks that the page shows the snapshot it gets on
// connecting, then each update: the status, the peers, the votes, the
// checks and the signing stats.
func TestPageLiveUpdates(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		e.must(e.expect("#d-block").ToHaveText(strconv.Itoa(pageHeight)))
		e.must(e.expect("#ns-consensus").ToHaveText(fmt.Sprintf("h=%d r=0 s=4", pageHeight+1)))
		e.must(e.expect(validatorEntryAt(p, valBravo.address())).ToHaveCount(1))
		e.must(e.expect(peerEntry(p, "eu-node")).ToHaveCount(1))
		e.must(e.expect("#map-card").ToBeVisible())
		e.must(e.expect("#d-apphash-last .hash-h").ToHaveText(fmt.Sprintf("(%d)", pageHeight)))

		next := healthyAt(pageHeight + 1)
		next.Peers = append(next.Peers, node.Peer{
			Moniker: "new-node", NodeID: nodeID("new-node"), RemoteIP: "203.0.113.20", Role: "full",
			Height: strconv.Itoa(pageHeight + 1),
		})
		next.AppHashLast = appHash(pageHeight + 1)
		next.Signing.ValidatorSigning[valDelta.address()] = node.ValSigning{
			Signed: 98, Eligible: 100, Missed: 2, Votes: 98, VoteDelayMs: 240,
		}
		e.publish(next)
		e.must(e.expect("#d-block").ToHaveText(strconv.Itoa(pageHeight + 1)))
		e.must(e.expect("#ns-consensus").ToHaveText(fmt.Sprintf("h=%d r=0 s=4", pageHeight+2)))
		e.must(e.expect(peerEntry(p, "new-node")).ToHaveCount(1))
		e.must(e.expect("#d-apphash-last .hash-h").ToHaveText(fmt.Sprintf("(%d)", pageHeight+1)))
		e.must(e.expect("#d-apphash-last .hash-hex").ToHaveText(appHashHex(pageHeight + 1)))
		e.must(e.expect("#recent-blocks").ToBeVisible())
		e.must(e.expect("#recent-blocks-body .info-btn").ToHaveCount(100))
		e.must(e.expect("#ns-active").ToHaveText("4/4"))
		e.must(e.expect("#ns-avgblock").ToHaveText("60.0s"))
	})
}

// TestPageSigningFromTheFirstMessage checks that the snapshot a page gets on
// connecting brings the signing stats: the recent blocks, the active
// validators and the average block time show without waiting for an
// update.
func TestPageSigningFromTheFirstMessage(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		e.must(e.expect("#recent-blocks").ToBeVisible())
		e.must(e.expect("#recent-blocks-body .info-btn").ToHaveCount(100))
		e.must(e.expect("#ns-active").ToHaveText("4/4"))
		e.must(e.expect("#ns-avgblock").ToHaveText("60.0s"))
	})
}

// TestPageHiddenSourcesFromTheFirstDraw checks that with --hide-sources the
// Peers list never shows a source node: the page reads the flag from the
// message that brings the peers, before it draws them.
func TestPageHiddenSourcesFromTheFirstDraw(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		sc.hideSources = true
		e := openPage(t, p, sc)
		e.must(e.expect(peerEntry(p, "eu-node")).ToHaveCount(1))
		e.must(e.expect(peerEntry(p, "sentry-1")).ToHaveCount(0))
	})
}

// TestPageVotesNameThePeers checks that a validator is matched to its peer by
// the peer's validator address: the peer's place and provider show in the
// validator's row or card, and the peer leaves the Peers list; a validator
// named by its address too.
func TestPageVotesNameThePeers(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		bravo := slices.IndexFunc(sc.snap.Peers, func(p node.Peer) bool { return p.Moniker == "bravo" })
		sc.snap.Peers[bravo].Moniker = "bravo-node"
		sc.snap.Peers[bravo].Country = "FR"
		sc.snap.Peers[bravo].Provider = "OVH"
		sc.snap.Peers = append(sc.snap.Peers, node.Peer{
			Moniker: "delta-node", NodeID: nodeID("delta-node"), RemoteIP: "203.0.113.15", Role: "val",
			ValAddress: valDelta.address(), Country: "JP", Provider: "Sakura",
		})
		e := openPage(t, p, sc)
		e.must(e.expect(peerEntry(p, "eu-node")).ToHaveCount(1))
		e.must(e.expect(peerEntry(p, "bravo-node")).ToHaveCount(0))
		e.must(e.expect(peerEntry(p, "delta-node")).ToHaveCount(0))
		for _, tc := range []struct{ address, country, cc, provider string }{
			{valBravo.address(), "🇫🇷 France", "FR", "OVH"},
			{valDelta.address(), "🇯🇵 Japan", "JP", "Sakura"},
		} {
			row := validatorEntryAt(p, tc.address)
			if p == phone {
				e.must(e.expect(row + " .peer-card-v >> nth=1").ToHaveText(tc.country))
				e.must(e.expect(row + " .peer-card-v >> nth=2").ToHaveText(tc.provider))
				continue
			}
			e.must(e.expect(row+" td.country-cell").ToHaveAttribute("data-cc", tc.cc))
			e.must(e.expect(row + " td:nth-child(4)").ToContainText(tc.provider))
		}
	})
}

// TestPageSourcesDownAndBack checks a page open while every source goes down
// and comes back: down, the tables and cards empty, the map hides, the
// consensus values read "-" and the boot panel warns; back, all of it
// shows again and the panel goes.
func TestPageSourcesDownAndBack(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork(), withInstalledClock())
		e.runFor(time.Second)
		liveExpectHealthy(e, p)

		e.publish(sourcesDown())
		e.runFor(readBootEvery)
		e.must(e.expect("#boot-panel").ToBeVisible())
		e.must(e.expect("#map-card").ToBeHidden())
		e.must(e.expect("#validators-header").ToHaveText("Validators (0)"))
		e.must(e.expect("#peers-header").ToHaveText("Peers (0)"))
		if p == phone {
			e.must(e.expect("#validators-cards .peer-card").ToHaveCount(0))
			e.must(e.expect("#peers-cards .peer-card").ToHaveCount(0))
		} else {
			e.must(e.expect("#validators-body").ToHaveText("no validators"))
			e.must(e.expect("#peers-body").ToHaveText("no peers"))
		}
		for _, id := range []string{"#ns-consensus", "#ns-proposer", "#ns-votes", "#ns-roundage", "#ns-active-vp"} {
			e.must(e.expect(id).ToHaveText("-"))
		}
		e.must(e.expect("#ns-active-vp").ToHaveClass("v"))

		e.publish(healthySnapshot())
		e.runFor(readBootEvery)
		liveExpectHealthy(e, p)
	})
}

// TestPageLoadedWhileSourcesDown checks a page loaded while every source is
// down: once the server's first message comes, the tables and cards are
// empty, not loading, and the boot panel warns.
func TestPageLoadedWhileSourcesDown(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, &scenario{now: pageNow, snap: sourcesDown()})
		e.must(e.expect("#boot-panel").ToBeVisible())
		e.must(e.expect("#validators-header").ToHaveText("Validators (0)"))
		e.must(e.expect("#peers-header").ToHaveText("Peers (0)"))
		if p != phone {
			e.must(e.expect("#validators-body").ToHaveText("no validators"))
			e.must(e.expect("#peers-body").ToHaveText("no peers"))
		}
		e.must(e.expect("#map-card").ToBeHidden())
	})
}

// TestPageVotingPowerWithoutPower checks that the votes line and the active
// voting power read "-" when the votes carry no voting power, or none they
// can read, rather than what they read before.
func TestPageVotingPowerWithoutPower(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*node.Snapshot)
	}{
		{"no power", func(snap *node.Snapshot) { snap.Validators = nil }},
		{"unreadable power", func(snap *node.Snapshot) {
			for i := range snap.Validators {
				snap.Validators[i].VotingPower = "lots"
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				e := openPage(t, p, healthyNetwork())
				e.must(e.expect("#ns-votes").ToHaveText("30/0 of 35"))
				e.must(e.expect("#ns-active-vp").ToHaveText("35/35 (margin: +11)"))
				snap := healthySnapshot()
				tc.change(snap)
				e.publish(snap)
				e.must(e.expect("#ns-consensus").ToHaveText(fmt.Sprintf("h=%d r=0 s=4", pageHeight+1)))
				e.must(e.expect("#ns-votes").ToHaveText("-"))
				e.must(e.expect("#ns-active-vp").ToHaveText("-"))
				e.must(e.expect("#ns-active-vp").ToHaveClass("v"))
			})
		})
	}
}

// liveExpectHealthy checks that the page shows the healthy network: its
// validators and peers, the map, the consensus, and no boot panel.
func liveExpectHealthy(e *pageEnv, p profile) {
	e.T.Helper()
	e.must(e.expect("#boot-panel").ToBeHidden())
	e.must(e.expect("#map-card").ToBeVisible())
	e.must(e.expect("#validators-header").ToHaveText("Validators (4)"))
	e.must(e.expect(validatorEntryAt(p, valBravo.address())).ToHaveCount(1))
	e.must(e.expect(peerEntry(p, "eu-node")).ToHaveCount(1))
	e.must(e.expect("#ns-consensus").ToHaveText(fmt.Sprintf("h=%d r=0 s=4", pageHeight+1)))
}

// ---- Rendering

// TestPageRenderWaitsForAFrame checks that the tables and the Network State
// draw a message at the next animation frame, with the data it brought.
func TestPageRenderWaitsForAFrame(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork(), withInstalledClock())
	e.runFor(time.Second)
	before := fmt.Sprintf("h=%d r=0 s=4", pageHeight+1)
	e.must(e.expect("#ns-consensus").ToHaveText(before))

	e.publish(healthyAt(pageHeight + 1))
	// The status shows as the message arrives; the rest waits for a frame.
	e.must(e.expect("#d-block").ToHaveText(strconv.Itoa(pageHeight + 1)))
	if got := liveText(e, "#ns-consensus"); got != before {
		t.Errorf("consensus before the frame = %q, want %q", got, before)
	}
	e.runFor(liveFrame)
	e.must(e.expect("#ns-consensus").ToHaveText(fmt.Sprintf("h=%d r=0 s=4", pageHeight+2)))
}

// TestPageUnchangedDataKeepsTheTables checks that a message changing
// nothing the tables and cards show leaves them as they are: the same
// elements, an open popover on one of them still open.
func TestPageUnchangedDataKeepsTheTables(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		target := charlieMissed100
		if p == phone {
			target = charlieCardMissed100
		}
		e.showPopover(e.Page.Locator(target))
		e.must(e.expect(pagePopover + " .t").ToHaveText("last 100 blocks"))
		old, err := e.Page.Locator(target).ElementHandle()
		e.must(err)

		snap := liveHeadAt(time.Second)
		e.publish(snap)
		e.must(e.expect("#d-time").ToContainText(snap.Status.SyncInfo.LatestBlockTime))
		liveAwaitFrame(e)
		if v, err := old.Evaluate(`(el) => el.isConnected`); err != nil || v != true {
			t.Fatalf("charlie's missed count is out of the page (%v, %v): the tables were drawn again", v, err)
		}
		e.must(e.expect(pagePopover).ToBeVisible())
		e.must(e.expect(pagePopover + " .t").ToHaveText("last 100 blocks"))
	})
}

// TestPageShownValuesFollowTheirData checks that each value the tables, the
// cards and the Network State show follows its data, whatever else the
// message changes: a message changing that value alone shows it.
func TestPageShownValuesFollowTheirData(t *testing.T) {
	bravo, charlie, delta := valBravo.address(), valCharlie.address(), valDelta.address()
	validator := func(name string, f func(*node.VoteInfo, *node.ValSigning)) func(*node.Snapshot) {
		return func(snap *node.Snapshot) {
			addr := map[string]string{"bravo": bravo, "charlie": charlie, "delta": delta}[name]
			i := slices.IndexFunc(snap.Consensus.Votes, func(v node.VoteInfo) bool { return v.Address == addr })
			sig := snap.Signing.ValidatorSigning[addr]
			f(&snap.Consensus.Votes[i], &sig)
			snap.Signing.ValidatorSigning[addr] = sig
		}
	}
	peer := func(moniker string, f func(*node.Peer)) func(*node.Snapshot) {
		return func(snap *node.Snapshot) {
			f(&snap.Peers[slices.IndexFunc(snap.Peers, func(p node.Peer) bool { return p.Moniker == moniker })])
		}
	}
	// cardValue is the value of key on the card selector matches.
	cardValue := func(card, key string) string {
		return card + fmt.Sprintf(` .peer-card-k:text-is(%q) + .peer-card-v`, key)
	}
	for _, tc := range []struct {
		name   string
		change func(*node.Snapshot)
		// table checks the tables (desktop), card the cards (phone); card
		// is nil when the cards do not show the value.
		table, card func(e *pageEnv)
	}{
		{
			name: "voting power",
			change: func(snap *node.Snapshot) {
				snap.Validators[slices.IndexFunc(snap.Validators, func(v node.Validator) bool { return v.Address == bravo })].VotingPower = "20"
			},
			table: func(e *pageEnv) {
				e.must(e.expect(validatorEntryAt(desktop, bravo) + " td:nth-child(5)").ToHaveText("20 (44%)"))
			},
			card: func(e *pageEnv) {
				e.must(e.expect(cardValue(validatorEntryAt(phone, bravo), "vp")).ToHaveText("20 (44%)"))
			},
		},
		{
			name:   "missed of the last 100",
			change: validator("delta", func(_ *node.VoteInfo, s *node.ValSigning) { s.Missed = 4 }),
			table: func(e *pageEnv) {
				e.must(e.expect(validatorEntryAt(desktop, delta) + " td:nth-child(7)").ToHaveText("4"))
			},
			card: func(e *pageEnv) {
				e.must(e.expect(cardValue(validatorEntryAt(phone, delta), "miss 100")).ToHaveText("4"))
			},
		},
		{
			name:   "vote delay",
			change: validator("charlie", func(_ *node.VoteInfo, s *node.ValSigning) { s.VoteDelayMs = 1500 }),
			table: func(e *pageEnv) {
				e.must(e.expect(validatorEntryAt(desktop, charlie) + " td:nth-child(9)").ToHaveText("+1.50 s"))
			},
			card: func(e *pageEnv) {
				e.must(e.expect(cardValue(validatorEntryAt(phone, charlie), "delay")).ToHaveText("+1.50 s"))
			},
		},
		{
			name:   "our validator",
			change: func(snap *node.Snapshot) { snap.Status.ValidatorInfo.Address = bravo },
			table:  func(e *pageEnv) { e.must(e.expect(validatorEntryAt(desktop, bravo) + " .source-tag").ToHaveCount(1)) },
			card:   func(e *pageEnv) { e.must(e.expect(validatorEntryAt(phone, bravo) + " .source-tag").ToHaveCount(1)) },
		},
		{
			name:   "proposer",
			change: func(snap *node.Snapshot) { snap.Consensus.Proposer = charlie },
			table:  func(e *pageEnv) { e.must(e.expect("#ns-proposer").ToHaveText("charlie")) },
			card:   func(e *pageEnv) { e.must(e.expect("#ns-proposer").ToHaveText("charlie")) },
		},
		{
			name:   "country",
			change: peer("eu-node", func(p *node.Peer) { p.Country = "FR" }),
			table:  func(e *pageEnv) { e.must(e.expect(euNodeCountry).ToHaveAttribute("data-cc", "FR")) },
			card: func(e *pageEnv) {
				e.must(e.expect(cardValue(peerEntry(phone, "eu-node"), "country")).ToHaveText("🇫🇷 France"))
			},
		},
		{
			name:   "provider",
			change: peer("us-node", func(p *node.Peer) { p.Provider = "Google" }),
			table: func(e *pageEnv) {
				e.must(e.expect(peerEntry(desktop, "us-node") + " td:nth-child(4)").ToContainText("Google"))
			},
			card: func(e *pageEnv) {
				e.must(e.expect(cardValue(peerEntry(phone, "us-node"), "provider")).ToHaveText("Google"))
			},
		},
		{
			name:   "IP",
			change: peer("us-node", func(p *node.Peer) { p.RemoteIP = "198.51.100.99" }),
			table: func(e *pageEnv) {
				e.must(e.expect(peerEntry(desktop, "us-node")+" td:nth-child(4) .info-btn").ToHaveAttribute("data-pop-rows", regexp.MustCompile(`198\.51\.100\.99`)))
			},
		},
		{
			name:   "peers of a peer",
			change: peer("eu-node", func(p *node.Peer) { p.NPeers = 9 }),
			table:  func(e *pageEnv) { e.must(e.expect(peerEntry(desktop, "eu-node") + " td:nth-child(5)").ToHaveText("9")) },
			card:   func(e *pageEnv) { e.must(e.expect(cardValue(peerEntry(phone, "eu-node"), "peers")).ToHaveText("9")) },
		},
		{
			name:   "RPC",
			change: peer("full-norpc", func(p *node.Peer) { p.RPCURL = "http://203.0.113.14:26657" }),
			table: func(e *pageEnv) {
				e.must(e.expect(peerEntry(desktop, "full-norpc") + " td:nth-child(6) .info-btn").ToHaveCount(1))
			},
			card: func(e *pageEnv) {
				e.must(e.expect(peerEntry(phone, "full-norpc") + " .peer-card-rpc-val").ToHaveText("✓ http://203.0.113.14:26657"))
			},
		},
		{
			name:   "RPC height",
			change: peer("eu-node", func(p *node.Peer) { p.Height = strconv.Itoa(pageHeight + 5) }),
			table: func(e *pageEnv) {
				e.must(e.expect(peerEntry(desktop, "eu-node")+" td:nth-child(6) .info-btn").ToHaveAttribute("data-pop-rows",
					regexp.MustCompile(fmt.Sprintf(`"%d \(synced\)"`, pageHeight+5))))
			},
		},
		{
			name:   "source",
			change: peer("full-norpc", func(p *node.Peer) { p.Source = true }),
			table:  func(e *pageEnv) { e.must(e.expect(peerEntry(desktop, "full-norpc") + " .source-tag").ToHaveCount(1)) },
			card:   func(e *pageEnv) { e.must(e.expect(peerEntry(phone, "full-norpc") + " .source-tag").ToHaveCount(1)) },
		},
		{
			name:   "moniker",
			change: peer("us-node", func(p *node.Peer) { p.Moniker = "us-east" }),
			table:  func(e *pageEnv) { e.must(e.expect(peerEntry(desktop, "us-east")).ToHaveCount(1)) },
			card:   func(e *pageEnv) { e.must(e.expect(peerEntry(phone, "us-east")).ToHaveCount(1)) },
		},
	} {
		profiles := []profile{desktop, phone}
		if tc.card == nil {
			profiles = profiles[:1]
		}
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, profiles, func(t *testing.T, p profile) {
				check := tc.table
				if p == phone {
					check = tc.card
				}
				e := openPage(t, p, healthyNetwork())
				e.must(e.expect(validatorEntryAt(p, valBravo.address())).ToHaveCount(1))
				snap := healthySnapshot()
				tc.change(snap)
				e.publish(snap)
				check(e)
			})
		})
	}
}

// TestPageStoredValuesFollowTheirData checks that each value the Validators
// table takes from the block history or from the alert detector follows it,
// nothing the table shows changing in the chain's snapshot: a validator's
// misses of the last day and its expected proposals, as a block is stored;
// how far back the misses reach, as an older block is, the snapshot the same
// in each; and a validator the detector flags down, its run of misses in the
// snapshot that case's only change.
func TestPageStoredValuesFollowTheirData(t *testing.T) {
	delta := valDelta.address()
	cell := func(col int) string {
		return fmt.Sprintf("%s td:nth-child(%d)", validatorEntryAt(desktop, delta), col)
	}
	since := func(h int64) string {
		return "missed in last 24h · history since " + healthyBlockTime(h).UTC().Format(time.RFC3339)
	}
	// store writes rec, a block of the healthy network's set, to the
	// history.
	store := func(e *pageEnv, rec blocks.Record) {
		rec.Time, rec.ValsetHash = healthyBlockTime(rec.Height), "HEALTHY"
		e.must(e.Srv.Blocks.Write(e.T.Context(), []blocks.Record{rec}))
	}
	for _, tc := range []struct {
		name string
		sc   func() *scenario
		// change changes what the server reads, and returns the snapshot
		// to publish.
		change        func(e *pageEnv) *node.Snapshot
		before, after func(e *pageEnv)
	}{
		{
			name: "missed of the last day",
			sc:   healthyNetwork,
			change: func(e *pageEnv) *node.Snapshot {
				// Made at round 1 by proposers not known: the block
				// counts no proposal, so the misses change alone.
				store(e, blocks.Record{
					Height: pageHeight + 1, Proposer: valAlpha.address(), Round: 1,
					Absences: []blocks.Absence{{Address: delta, Kind: blocks.Absent}},
				})
				return healthySnapshot()
			},
			before: func(e *pageEnv) { e.must(e.expect(cell(valColMissed24h)).ToHaveText("1")) },
			after:  func(e *pageEnv) { e.must(e.expect(cell(valColMissed24h)).ToHaveText("2")) },
		},
		{
			name: "expected proposals",
			sc:   healthyNetwork,
			change: func(e *pageEnv) *node.Snapshot {
				// A block delta made at round 0: its expected proposals
				// change alone, where a miss would change both counts.
				store(e, blocks.Record{Height: pageHeight + 1, Proposer: delta})
				return healthySnapshot()
			},
			before: func(e *pageEnv) { e.must(e.expect(cell(valColProps)).ToHaveText("0 / 0")) },
			after:  func(e *pageEnv) { e.must(e.expect(cell(valColProps)).ToHaveText("0 / 1")) },
		},
		{
			name: "history since",
			sc: func() *scenario {
				// The history starts at height 2, its first block left
				// for the test to store.
				sc := healthyNetwork()
				set := members(healthyValidators()...)
				sc.seed = func(t *testing.T, s *blocks.Store) {
					putBlocks(t, s, healthyBlockTime, "HEALTHY", set, 2, pageHeight, healthyAbsences())
				}
				return sc
			},
			change: func(e *pageEnv) *node.Snapshot {
				store(e, blocks.Record{Height: 1, Proposer: valAlpha.address()})
				return healthySnapshot()
			},
			before: func(e *pageEnv) {
				e.must(e.expect(cell(valColMissed24h)).ToHaveAttribute("data-popover", since(2)))
			},
			after: func(e *pageEnv) {
				e.must(e.expect(cell(valColMissed24h)).ToHaveAttribute("data-popover", since(1)))
			},
		},
		{
			name: "down",
			sc: func() *scenario {
				sc := healthyNetwork()
				sc.push = true
				return sc
			},
			change: func(e *pageEnv) *node.Snapshot {
				snap := healthySnapshot()
				sig := snap.Signing.ValidatorSigning[delta]
				sig.MissedInARow, sig.SignedInARow = pageMaxMissedInARow, 0
				snap.Signing.ValidatorSigning[delta] = sig
				return snap
			},
			before: func(e *pageEnv) {
				e.must(e.expect(cell(valColMissed)).ToHaveClass(regexp.MustCompile(`\bwarn\b`)))
			},
			after: func(e *pageEnv) {
				e.must(e.expect(cell(valColMissed)).ToHaveClass(regexp.MustCompile(`\berr\b`)))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := openPage(t, desktop, tc.sc())
			tc.before(e)
			e.publish(tc.change(e))
			tc.after(e)
		})
	}
}

// TestPageUnshownChangesKeepTheTables checks that a message changing only
// what the tables and cards do not show leaves them as they are, an open
// popover on them open: a new round with new votes, the peers' own rounds,
// a new head. The Network State shows the new round and keeps an unchanged
// proposer, an open popover on it open too.
func TestPageUnshownChangesKeepTheTables(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		missed := charlieMissed100
		if p == phone {
			missed = charlieCardMissed100
		}
		for _, tc := range []struct{ name, target, shows string }{
			{"table", missed, " .t:text-is('last 100 blocks')"},
			{"proposer", "#ns-proposer .tap-popover", ":text-is('" + valDelta.address() + "')"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				sc := healthyNetwork()
				sc.snap.Consensus.Proposer = valDelta.address()
				e := openPage(t, p, sc)
				e.showPopover(e.Page.Locator(tc.target))
				e.must(e.expect(pagePopover + tc.shows).ToBeVisible())
				old, err := e.Page.Locator(tc.target).ElementHandle()
				e.must(err)

				next := healthyAt(pageHeight + 1)
				next.Consensus.Proposer = valDelta.address()
				next.Consensus.Step = "6"
				next.Consensus.RoundStartTime = pageNow.Add(-time.Second).Format(time.RFC3339Nano)
				for i := range next.Consensus.Votes {
					next.Consensus.Votes[i].Prevoted, next.Consensus.Votes[i].Precommit = true, true
				}
				for i := range next.Peers {
					next.Peers[i].PeerHeight, next.Peers[i].PeerRound, next.Peers[i].PeerStep = strconv.Itoa(pageHeight+2), "0", "6"
					next.Peers[i].LastSeen = pageNow.Format(time.RFC3339)
				}
				next.Status.SyncInfo.LatestBlockTime = pageNow.Add(-time.Second).Format(time.RFC3339Nano)
				e.publish(next)
				e.must(e.expect("#ns-consensus").ToHaveText(fmt.Sprintf("h=%d r=0 s=6", pageHeight+2)))
				e.must(e.expect("#ns-votes").ToHaveText("35/35 of 35"))
				if v, err := old.Evaluate(`(el) => el.isConnected`); err != nil || v != true {
					t.Fatalf("the popover's target is out of the page (%v, %v): it was drawn again", v, err)
				}
				e.must(e.expect(pagePopover + tc.shows).ToBeVisible())
			})
		}
	})
}

// TestPageMapKeepsItsDrawing checks that a message changing nothing the map
// and its side list draw leaves them as they are: an open cluster's popover
// stays open, a scrolled list stays scrolled. A change they draw draws them
// again.
func TestPageMapKeepsItsDrawing(t *testing.T) {
	// liveWorld is the healthy network seeing the world's peers, the first
	// with n peers of its own: the Peers table shows n, the map does not.
	liveWorld := func(n int) *node.Snapshot {
		snap := healthySnapshot()
		snap.Peers = worldPeers()
		snap.Peers[0].NPeers = n
		return snap
	}
	const list = "#map-country-list"
	firstPeers := peerEntry(desktop, "node-us-0") + " td:nth-child(5)"
	t.Run("popover", func(t *testing.T) {
		sc := healthyNetwork()
		sc.snap = liveWorld(1)
		e := openPage(t, desktop, sc)
		dot := e.Page.Locator(fmt.Sprintf(`#map-points [data-cc="%s"]`, loneCountry))
		e.showPopover(dot)
		e.must(e.expect(pagePopover + " .t").ToContainText("New Zealand"))
		old, err := dot.ElementHandle()
		e.must(err)
		e.publish(liveWorld(2))
		e.must(e.expect(firstPeers).ToHaveText("2"))
		if v, err := old.Evaluate(`(el) => el.isConnected`); err != nil || v != true {
			t.Fatalf("the cluster is out of the page (%v, %v): the map was drawn again", v, err)
		}
		e.must(e.expect(pagePopover).ToBeVisible())
		e.must(e.expect(pagePopover + " .t").ToContainText("New Zealand"))
	})
	t.Run("list", func(t *testing.T) {
		sc := healthyNetwork()
		sc.snap = liveWorld(1)
		e := openPage(t, desktop, sc)
		e.must(e.expect(list + " .country-row").ToHaveCount(40))
		b := boxOf(e, list)
		e.must(e.Page.Mouse().Move(b.X+b.Width/2, b.Y+b.Height/2))
		e.must(e.Page.Mouse().Wheel(0, 120))
		if _, err := e.Page.WaitForFunction(`(l) => document.querySelector(l).scrollTop === 120`, list); err != nil {
			t.Fatalf("the wheel left the list at scrollTop %d, want 120: %v", liveScrollTop(e, list), err)
		}
		e.publish(liveWorld(2))
		e.must(e.expect(firstPeers).ToHaveText("2"))
		if got := liveScrollTop(e, list); got != 120 {
			t.Errorf("the list scrolled to %d after a message, want 120", got)
		}

		moved := liveWorld(2)
		moved.Peers[slices.IndexFunc(moved.Peers, func(p node.Peer) bool { return p.Country == loneCountry })].Country = "AU"
		e.publish(moved)
		e.must(e.expect(fmt.Sprintf(`#map-points [data-cc="%s"]`, loneCountry)).ToHaveCount(0))
		e.must(e.expect(list + " .country-row").ToHaveCount(39))
	})
}

// liveScrollTop reads how far the element selector matches is scrolled
// down.
func liveScrollTop(e *pageEnv, selector string) int {
	e.T.Helper()
	return evalInt(e, fmt.Sprintf(`() => document.querySelector(%q).scrollTop`, selector))
}

// TestPageRedrawKeepsTheScroll checks that drawing the tables again keeps
// where the page and the Validators table are scrolled to.
func TestPageRedrawKeepsTheScroll(t *testing.T) {
	// The tablet shows the table in its 481–768 band once compact is set.
	e := openPage(t, tablet, healthyNetwork(), withStorage(map[string]string{"validators-compact": "true"}))
	const wrap = "#validators-card .table-wrap"
	e.must(e.expect("#validators-body tr").ToHaveCount(4))
	// The table is below the fold: showing it scrolls the page.
	e.must(e.Page.Locator(wrap).ScrollIntoViewIfNeeded())
	b := boxOf(e, wrap)
	e.must(e.Page.Mouse().Move(b.X+b.Width/2, b.Y+b.Height/2))
	// The wheel scrolls the table to its end, through an animation: the
	// test waits for the end.
	e.must(e.Page.Mouse().Wheel(200, 0))
	if _, err := e.Page.WaitForFunction(`(w) => {
		const el = document.querySelector(w)
		return el.scrollLeft > 0 && el.scrollLeft === el.scrollWidth - el.clientWidth
	}`, wrap); err != nil {
		t.Fatalf("the wheel scrolled the table to %d, want its end: %v", liveScrollLeft(e, wrap), err)
	}
	if evalInt(e, `() => scrollY`) == 0 {
		t.Fatal("the page is at its top: showing the table did not scroll it")
	}
	y, x := evalInt(e, `() => scrollY`), liveScrollLeft(e, wrap)

	e.publish(nextBlock())
	e.must(e.expect("#ns-consensus").ToContainText(fmt.Sprintf("h=%d ", pageHeight+2)))
	if got := evalInt(e, `() => scrollY`); got != y {
		t.Errorf("page scrolled to %d after the redraw, want %d", got, y)
	}
	if got := liveScrollLeft(e, wrap); got != x {
		t.Errorf("Validators table scrolled to %d after the redraw, want %d", got, x)
	}
}

// liveScrollLeft reads how far the element selector matches is scrolled
// sideways.
func liveScrollLeft(e *pageEnv, selector string) int {
	e.T.Helper()
	return evalInt(e, fmt.Sprintf(`() => document.querySelector(%q).scrollLeft`, selector))
}

// ---- Network State values

// TestPageRoundAge checks the round age: its format by size, and its colour
// past 10 s and 30 s; a round starting later than now reads 0s, one with no
// valid start "-".
func TestPageRoundAge(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		expectValue(e, "#ns-roundage", "4s", "v ok")
		for i, tc := range []struct {
			age         time.Duration
			text, class string
		}{
			{10 * time.Second, "10s", "v ok"},
			{11 * time.Second, "11s", "v warn"},
			{30 * time.Second, "30s", "v warn"},
			{31 * time.Second, "31s", "v err"},
			{90 * time.Second, "1m 30s", "v err"},
			{2*time.Hour + 5*time.Minute + 9*time.Second, "2h 5m", "v err"},
			{76*time.Hour + 30*time.Minute, "3d 4h", "v err"},
			{-5 * time.Second, "0s", "v ok"},
		} {
			e.publish(liveRound(i+1, tc.age))
			e.must(e.expect("#ns-consensus").ToHaveText(fmt.Sprintf("h=%d r=%d s=4", pageHeight+1, i+1)))
			expectValue(e, "#ns-roundage", tc.text, tc.class)
		}
		for i, start := range []string{"", "soon"} {
			snap := liveRound(100+i, 0)
			snap.Consensus.RoundStartTime = start
			e.publish(snap)
			e.must(e.expect("#ns-consensus").ToHaveText(fmt.Sprintf("h=%d r=%d s=4", pageHeight+1, 100+i)))
			expectValue(e, "#ns-roundage", "-", "v")
		}
	})
}

// TestPageRoundAgeTicks checks that the round age counts the seconds with no
// message coming, as on a halted chain, through its colours; the seconds
// going by do not flash it.
func TestPageRoundAgeTicks(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork(), withInstalledClock())
		const age = "#ns-roundage"
		// The first value flashes: the frames that draw it and start the
		// flash.
		e.runFor(100 * time.Millisecond)
		expectValue(e, age, "4s", "v ok")
		flash := runningFlash(e, age)
		e.runFor(900 * time.Millisecond)
		expectValue(e, age, "5s", "v ok")
		if state := flashState(e, flash); state == "idle" {
			t.Errorf("the round age's flash is idle after a second went by, want it left running")
		}
		e.runFor(6 * time.Second)
		expectValue(e, age, "11s", "v warn")
		e.runFor(20 * time.Second)
		expectValue(e, age, "31s", "v err")
	})
}

// TestPageRoundAgeFlashesForANewRound checks that a message flashes the round
// age only when it starts a new round: on a halted chain, a message of the
// same round leaves the running flash alone, whatever age it brings; the
// next round flashes it again.
func TestPageRoundAgeFlashesForANewRound(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork(), withInstalledClock())
		const age = "#ns-roundage"
		e.runFor(100 * time.Millisecond)
		expectValue(e, age, "4s", "v ok")
		e.runFor(5 * time.Second)
		expectValue(e, age, "9s", "v ok")
		flash := runningFlash(e, age)
		// The server's clock steps 5 s back, as a clock correction on its
		// host does: its message sets the page's clock, and the age, back
		// to 4s.
		e.clock.advance(-5 * time.Second)
		e.publish(healthySnapshot())
		expectValue(e, age, "4s", "v ok")
		if state := flashState(e, flash); state == "idle" {
			t.Errorf("the round age's flash is idle after a message of the same round, want it left running")
		}

		e.publish(liveRound(1, 2*time.Second))
		awaitFlashCancelled(e, flash, "round 1")
		e.runFor(100 * time.Millisecond)
		expectValue(e, age, "2s", "v ok")
		e.must(e.expect(age).ToHaveClass(regexp.MustCompile(`\bchanged\b`)))
	})
}

// TestPageBlockHeight checks the head's height: green, red at 0, with a
// stuck tag while the round has run more than 30 s, and red as its tag then.
func TestPageBlockHeight(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		const block, tag = "#d-block", "#d-block .tag-red"
		expectValue(e, block, strconv.Itoa(pageHeight), "v ok")
		e.must(e.expect(tag).ToHaveCount(0))

		zero := healthySnapshot()
		zero.Status.SyncInfo.LatestBlockHeight = "0"
		e.publish(zero)
		expectValue(e, block, "0", "v err")

		for i, tc := range []struct {
			age   time.Duration
			stuck bool
		}{
			{31 * time.Second, true},
			{30 * time.Second, false},
			// 30 whole seconds, which the round age shows: not stuck yet.
			{30*time.Second + 500*time.Millisecond, false},
			{40 * time.Second, true},
		} {
			e.publish(liveRound(i+1, tc.age))
			e.must(e.expect("#ns-consensus").ToHaveText(fmt.Sprintf("h=%d r=%d s=4", pageHeight+1, i+1)))
			if !tc.stuck {
				e.must(e.expect(tag).ToHaveCount(0))
				expectValue(e, block, strconv.Itoa(pageHeight), "v ok")
				continue
			}
			e.must(e.expect(tag).ToHaveText(" stuck"))
			expectValue(e, block, strconv.Itoa(pageHeight)+" stuck", "v err")
		}
	})
}

// TestPageStuckTagReadsItsMessage checks that the stuck tag follows the votes
// of the message that brings the status: on the first message, and at once
// when a round runs past 30 s or a new one starts.
func TestPageStuckTagReadsItsMessage(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		const tag = "#d-block .tag-red"
		sc := healthyNetwork()
		sc.snap = liveRound(0, 40*time.Second)
		e := openPage(t, p, sc)
		e.must(e.expect(tag).ToHaveText(" stuck"))
		e.publish(liveRound(1, 4*time.Second))
		e.must(e.expect("#ns-roundage").ToHaveText("4s"))
		e.must(e.expect(tag).ToHaveCount(0))
		e.publish(liveRound(1, 31*time.Second))
		e.must(e.expect(tag).ToHaveText(" stuck"))
	})
}

// TestPageHeadAgesTick checks that the head block's age and the stuck tag
// follow the seconds with no message coming: the age counts up, and the tag
// comes once the round has run past 30 s, the height turning red with it.
func TestPageHeadAgesTick(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		const tag = "#d-block .tag-red"
		e := openPage(t, p, healthyNetwork(), withInstalledClock())
		e.runFor(100 * time.Millisecond)
		e.must(e.expect("#d-time .ago").ToHaveText(" (2s ago)"))
		e.runFor(3 * time.Second)
		e.must(e.expect("#d-time .ago").ToHaveText(" (5s ago)"))
		e.runFor(23 * time.Second)
		expectValue(e, "#ns-roundage", "30s", "v warn")
		e.must(e.expect(tag).ToHaveCount(0))
		expectValue(e, "#d-block", strconv.Itoa(pageHeight), "v ok")
		e.runFor(time.Second)
		expectValue(e, "#ns-roundage", "31s", "v err")
		e.must(e.expect(tag).ToHaveText(" stuck"))
		expectValue(e, "#d-block", strconv.Itoa(pageHeight)+" stuck", "v err")
		e.must(e.expect("#d-time .ago").ToHaveText(" (29s ago)"))
	})
}

// TestPageAgesReadTheServerClock checks that every age reads the server's
// clock, with the browser's 10 minutes behind it: the round's, the head
// block's, the chain's and the stuck tag read as on the server, a head block
// later than the server's clock reads 0 s ago, the countdown to genesis
// counts to the server's genesis, and the drift warning says 600s.
func TestPageAgesReadTheServerClock(t *testing.T) {
	behind := withBrowserOffset(-10 * time.Minute)
	t.Run("live", func(t *testing.T) {
		onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
			e := openPage(t, p, healthyNetwork(), behind)
			expectValue(e, "#ns-roundage", "4s", "v ok")
			e.must(e.expect("#d-time .ago").ToHaveText(" (2s ago)"))
			e.must(e.expect("#c-gentime .ago").ToHaveText(" (3d 0h ago)"))
			e.must(e.expect("#d-block .tag-red").ToHaveCount(0))
			e.must(e.expect("#gc-drift").ToContainText("600s"))

			e.publish(liveRound(1, 40*time.Second))
			expectValue(e, "#ns-roundage", "40s", "v err")
			e.must(e.expect("#d-block .tag-red").ToHaveText(" stuck"))

			e.publish(liveHeadAt(-3 * time.Second))
			e.must(e.expect("#d-time .ago").ToHaveText(" (0s ago)"))
		})
	})
	t.Run("before genesis", func(t *testing.T) {
		onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
			e := openPage(t, p, liveBeforeGenesis(time.Hour+5*time.Second), behind)
			e.must(e.expect("#gc-countdown").ToHaveText("1h 5s"))
			e.must(e.expect("#gc-nodetime").ToHaveText("12:00:00Z"))
			e.must(e.expect("#gc-drift").ToHaveText("⚠ clock drift: 600s between your browser and the node — consider ntpdate"))
			e.must(e.expect("#gc-drift").ToBeVisible())
		})
	})
}

// TestPageBlockTime checks the head's time and how long ago it was made, in
// its two largest units, as the round and the chain's ages: seconds, minutes
// and seconds, hours and minutes, or days and hours; "-" with no block yet.
func TestPageBlockTime(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		head := healthyBlockTime(pageHeight).Format(time.RFC3339Nano)
		expectValue(e, "#d-time", head+" (2s ago)", "v")
		e.must(e.expect("#d-time .ago").ToHaveText(" (2s ago)"))

		for _, tc := range []struct {
			age  time.Duration
			want string
		}{
			{59 * time.Second, " (59s ago)"},
			{time.Minute, " (1m 0s ago)"},
			{time.Hour - time.Second, " (59m 59s ago)"},
			{time.Hour, " (1h 0m ago)"},
			{24*time.Hour - time.Second, " (23h 59m ago)"},
			{24 * time.Hour, " (1d 0h ago)"},
			{50 * time.Hour, " (2d 2h ago)"},
		} {
			snap := liveHeadAt(tc.age)
			e.publish(snap)
			expectValue(e, "#d-time", snap.Status.SyncInfo.LatestBlockTime+tc.want, "v")
		}

		for _, tc := range []struct{ name, height, time string }{
			{"height 0", "0", head},
			{"time of 1970", strconv.Itoa(pageHeight), "1970-01-01T00:00:00Z"},
		} {
			snap := healthySnapshot()
			snap.Status.SyncInfo.LatestBlockHeight = tc.height
			snap.Status.SyncInfo.LatestBlockTime = tc.time
			e.publish(snap)
			e.must(e.expect("#d-block").ToHaveText(tc.height))
			expectValue(e, "#d-time", "-", "v")
		}
	})
}

// TestPageCopyTimesAlone checks that a click on the block height, the block
// time or the genesis time copies the value alone, without its age or its
// stuck tag.
func TestPageCopyTimesAlone(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		sc.snap = liveRound(0, 40*time.Second)
		e := openPage(t, p, sc, withInstalledClock())
		e.runFor(time.Second)
		if p == phone {
			// Compact mode, on by default on a phone, hides the height and
			// the genesis time.
			e.must(e.Page.Locator("#ns-compact-cb").Uncheck())
		}
		e.must(e.expect("#d-block .tag-red").ToHaveText(" stuck"))
		for _, tc := range []struct{ value, want string }{
			{"#d-block", strconv.Itoa(pageHeight)},
			{"#d-time", healthyBlockTime(pageHeight).Format(time.RFC3339Nano)},
			{"#c-gentime", healthyBlockTime(0).Format(nodeTimeLayout)},
		} {
			e.must(e.expect(tc.value + " .tag-red, " + tc.value + " .ago").ToHaveCount(1))
			e.press(e.Page.Locator(tc.value))
			expectToast(e, "Copied!")
			// Only Chromium is granted reading the clipboard.
			if p.browser == chromium {
				if got := e.clipboard(); got != tc.want {
					t.Errorf("click on %s: clipboard = %q, want %q", tc.value, got, tc.want)
				}
			}
			fadeToast(e)
		}
	})
}

// TestPageClockDrift checks the drift warning of a chain before its genesis:
// hidden within 2 s of the server's clock, yellow up to 10 s, red beyond.
func TestPageClockDrift(t *testing.T) {
	const yellow, red = "rgb(210, 153, 34)", "rgb(248, 81, 73)"
	for _, tc := range []struct {
		name   string
		offset time.Duration // the browser's clock from the server's
		drift  string        // "" when hidden
		color  string
	}{
		{"same clock", 0, "", ""},
		{"2 s behind", -2 * time.Second, "", ""},
		{"3 s behind", -3 * time.Second, "3s", yellow},
		{"10 s ahead", 10 * time.Second, "10s", yellow},
		{"11 s behind", -11 * time.Second, "11s", red},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := openPage(t, desktop, liveBeforeGenesis(time.Hour), withBrowserOffset(tc.offset))
			e.must(e.expect("#genesis-countdown").ToBeVisible())
			drift := e.expect("#gc-drift")
			if tc.drift == "" {
				e.must(e.expect("#gc-time").ToHaveText(pageNow.Add(time.Hour).Format(nodeTimeLayout)))
				e.must(drift.ToBeHidden())
				return
			}
			e.must(drift.ToBeVisible())
			e.must(drift.ToHaveText("⚠ clock drift: " + tc.drift + " between your browser and the node — consider ntpdate"))
			e.must(drift.ToHaveCSS("color", tc.color))
		})
	}
	t.Run("phone", func(t *testing.T) {
		e := openPage(t, phone, liveBeforeGenesis(time.Hour), withBrowserOffset(-11*time.Second))
		e.must(e.expect("#gc-drift").ToBeVisible())
		e.must(e.expect("#gc-drift").ToContainText("11s"))
	})
	t.Run("after genesis", func(t *testing.T) {
		e := openPage(t, desktop, healthyNetwork(), withBrowserOffset(-11*time.Second))
		e.must(e.expect("#gc-drift").ToContainText("11s"))
		e.must(e.expect("#genesis-countdown").ToBeHidden())
		e.must(e.expect("#gc-drift").ToBeHidden())
	})
}

// TestPageServerTimeOnConnect checks that the page sets its clock by the
// server's time when the message was sent, not when its snapshot was
// fetched: a snapshot fetched 5 s before the page connects shows no drift,
// and the node's time is the server's.
func TestPageServerTimeOnConnect(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := liveBeforeGenesis(time.Hour)
		sc.snap.System.NodeTime = pageNow.Add(-5 * time.Second).Format(nodeTimeLayout)
		e := openPage(t, p, sc)
		e.must(e.expect("#genesis-countdown").ToBeVisible())
		e.must(e.expect("#gc-countdown").ToHaveText("1h 0s"))
		e.must(e.expect("#gc-nodetime").ToHaveText("12:00:00Z"))
		e.must(e.expect("#gc-drift").ToBeHidden())
	})
}

// TestPageGenesisCountdown checks a chain before its genesis: the banner
// counts down in hours, minutes and seconds, a zero minute left out, with
// the node's time; the genesis time reads when the chain starts, ticking.
// At genesis the banner goes and the chain's age counts up.
func TestPageGenesisCountdown(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		in := time.Hour + 5*time.Second
		e := openPage(t, p, liveBeforeGenesis(in), withInstalledClock())
		genesis := pageNow.Add(in).Format(nodeTimeLayout)
		e.runFor(time.Second)
		e.must(e.expect("#genesis-countdown").ToBeVisible())
		e.must(e.expect("#gc-countdown").ToHaveText("1h 4s"))
		e.must(e.expect("#gc-time").ToHaveText(genesis))
		e.must(e.expect("#gc-nodetime").ToHaveText("12:00:01Z"))
		e.must(e.expect("#c-gentime").ToHaveText(genesis + " (starts in 1h 4s)"))

		e.runFor(4 * time.Second)
		e.must(e.expect("#gc-countdown").ToHaveText("1h 0s"))
		e.must(e.expect("#gc-nodetime").ToHaveText("12:00:05Z"))
		e.fastForward(time.Hour - 2*time.Second)
		e.runFor(time.Second)
		e.must(e.expect("#gc-countdown").ToHaveText("1s"))
		e.runFor(time.Second)
		e.must(e.expect("#genesis-countdown").ToBeHidden())
		e.must(e.expect("#c-gentime").ToHaveText(genesis + " (0s ago)"))
		e.runFor(time.Second)
		e.must(e.expect("#c-gentime .ago").ToHaveText(" (1s ago)"))
	})
}

// TestPageChainAge checks how long ago a started chain's genesis was, in
// seconds, minutes and seconds, hours and minutes, or days and hours.
func TestPageChainAge(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		genesis := healthyBlockTime(0).Format(nodeTimeLayout)
		e.must(e.expect("#c-gentime").ToHaveText(genesis + " (3d 0h ago)"))
		e.must(e.expect("#genesis-countdown").ToBeHidden())
		for _, tc := range []struct {
			ago  time.Duration
			want string
		}{
			{30 * time.Second, "30s ago"},
			{90 * time.Second, "1m 30s ago"},
			{2*time.Hour + 5*time.Minute + 9*time.Second, "2h 5m ago"},
			{76*time.Hour + 30*time.Minute, "3d 4h ago"},
		} {
			snap := liveGenesisIn(-tc.ago)
			e.publish(snap)
			e.must(e.expect("#c-gentime").ToHaveText(snap.System.GenesisTime + " (" + tc.want + ")"))
		}
	})
}
