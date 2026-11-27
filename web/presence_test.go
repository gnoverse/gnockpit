package web

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/presence"
)

// openPresenceDB opens an in-memory presence store and its database, both
// closed with the test.
func openPresenceDB(t *testing.T) (*presence.Store, *sql.DB) {
	t.Helper()
	db := openTestDB(t)
	store, err := presence.NewStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return store, db
}

// openPresenceStore opens an in-memory presence store, closed with the test.
func openPresenceStore(t *testing.T) *presence.Store {
	t.Helper()
	store, _ := openPresenceDB(t)
	return store
}

// presenceSnapshot is a snapshot of chain test-x listing the peers n1, n2 and
// one without a node ID.
func presenceSnapshot(peersRead bool) *node.Snapshot {
	snap := seamSnapshot("42", seamNow)
	snap.Peers = []node.Peer{{NodeID: "n1"}, {NodeID: "n2"}, {Moniker: "anonymous"}}
	snap.PeersRead = peersRead
	return snap
}

func recordPoll(t *testing.T, store *presence.Store, at time.Time, ids ...string) {
	t.Helper()
	if err := store.Record(t.Context(), "test-x", at, ids); err != nil {
		t.Fatalf("Record(%s, %v): %v", at, ids, err)
	}
}

// netInfoSource serves a source node of chain test-x: its /status answers with
// id as the node id, at height 1 (no block to read), and its validator set is
// empty. Its /net_info lists the peer "<id>-peer" when netInfo is true and
// fails otherwise.
func netInfoSource(t *testing.T, id string, netInfo bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/status":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"node_info":{"moniker":%q,"network":"test-x","net_address":"%s@127.0.0.1:26656"},
				"sync_info":{"latest_block_height":"1","latest_block_time":"2026-11-18T12:00:00Z"}}}`, id, id)
		case r.URL.Path == "/validators":
			fmt.Fprint(w, `{"jsonrpc":"2.0","result":{"validators":[]}}`)
		case r.URL.Path == "/net_info" && netInfo:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"peers":[{"node_info":{"moniker":"%[1]s-peer","id":"%[1]s-peer"},"remote_ip":"10.0.0.5"}]}}`, id)
		default:
			http.Error(w, "not served", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestFetchSnapshotReadsThePeersWhenANetInfoAnswers checks that a fetched
// snapshot has read the peers, which makes publish record its poll, when any
// source's /net_info answered, and has not when none did; a peer only a
// failing source lists is not listed, and each failure is logged.
func TestFetchSnapshotReadsThePeersWhenANetInfoAnswers(t *testing.T) {
	for _, tc := range []struct {
		name      string
		netInfos  []bool // per source, whether its /net_info answers
		wantRead  bool
		wantPeers []string
	}{
		{"its net_info answers", []bool{true}, true, []string{"s0", "s0-peer"}},
		{"its net_info fails", []bool{false}, false, []string{"s0"}},
		{"one of two net_infos answers", []bool{false, true}, true, []string{"s0", "s1", "s1-peer"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSeamServer(t)
			logs := captureLog(t)
			var urls, wantLogs []string
			for i, answers := range tc.netInfos {
				src := netInfoSource(t, fmt.Sprintf("s%d", i), answers)
				urls = append(urls, src.URL)
				if !answers {
					wantLogs = append(wantLogs, "net_info from "+src.URL+": ")
				}
			}
			s.Sources = node.NewSources(urls, time.Second, s.Client.Names, nil)

			snap := s.fetchSnapshot(t.Context())

			if snap.PeersRead != tc.wantRead {
				t.Errorf("PeersRead = %v, want %v", snap.PeersRead, tc.wantRead)
			}
			var peers []string
			for _, p := range snap.Peers {
				peers = append(peers, p.NodeID)
			}
			slices.Sort(peers)
			if !slices.Equal(peers, tc.wantPeers) {
				t.Errorf("peers = %v, want %v", peers, tc.wantPeers)
			}
			var lines []string
			if out := strings.TrimSpace(logs.String()); out != "" {
				lines = strings.Split(out, "\n")
			}
			if len(lines) != len(wantLogs) {
				t.Fatalf("log = %q, want a line per failing net_info: %q", logs.String(), wantLogs)
			}
			for i, want := range wantLogs {
				if !strings.Contains(lines[i], want) {
					t.Errorf("log line %d = %q, want it to contain %q", i, lines[i], want)
				}
			}
		})
	}
}

func TestPublishRecordsAPollThatReadThePeers(t *testing.T) {
	s := newSeamServer(t)
	s.Presence = openPresenceStore(t)
	snap := presenceSnapshot(true)

	s.publish(t.Context(), snap)

	want := &node.Presence{Listed: 1, Polls: 1, FirstSeen: "2026-11-18T12:00:00Z", FromStart: true}
	if got := snap.Peers[0].Presence; got == nil || *got != *want {
		t.Errorf("n1 presence = %+v, want %+v", got, want)
	}
	if got := snap.Peers[2].Presence; got != nil {
		t.Errorf("presence of the peer without a node id = %+v, want nil", got)
	}
}

func TestPublishGivesEachPeerItsPresence(t *testing.T) {
	s := newSeamServer(t)
	s.Presence = openPresenceStore(t)
	recordPoll(t, s.Presence, seamNow.Add(-time.Hour), "n1")
	recordPoll(t, s.Presence, seamNow.Add(-30*time.Minute), "n1", "n2")
	snap := presenceSnapshot(false)

	s.publish(t.Context(), snap)

	n1 := node.Presence{Listed: 2, Polls: 2, FirstSeen: seamNow.Add(-time.Hour).Format(time.RFC3339), FromStart: true}
	n2 := node.Presence{Listed: 1, Polls: 2, FirstSeen: seamNow.Add(-30 * time.Minute).Format(time.RFC3339)}
	// What /ws and /api/status send carries each peer's presence.
	live := s.buildLiveData(s.getSnapshot()).Peers
	status := s.buildStatusReport(s.getSnapshot(), seamNow).Peers
	for i, want := range []node.Presence{n1, n2} {
		if got := live[i].Presence; got == nil || *got != want {
			t.Errorf("/ws peer %d presence = %+v, want %+v", i, got, want)
		}
		if got := status[i].Presence; got == nil || *got != want {
			t.Errorf("/api/status peer %d presence = %+v, want %+v", i, got, want)
		}
	}
}

func TestPublishWithoutAStoreLeavesPresenceOut(t *testing.T) {
	s := newSeamServer(t)
	logs := captureLog(t)
	snap := presenceSnapshot(true)

	s.publish(t.Context(), snap)

	for i, p := range snap.Peers {
		if p.Presence != nil {
			t.Errorf("peer %d presence = %+v without a store, want nil", i, p.Presence)
		}
	}
	if logs.Len() != 0 {
		t.Errorf("log = %q, want empty", logs.String())
	}
}

func TestPublishRecordsNoPollWithoutThePeersRead(t *testing.T) {
	s := newSeamServer(t)
	s.Presence = openPresenceStore(t)
	recordPoll(t, s.Presence, seamNow.Add(-time.Hour), "n1")
	logs := captureLog(t)

	snap := presenceSnapshot(false)
	s.publish(t.Context(), snap)
	if got := snap.Peers[0].Presence; got == nil || got.Listed != 1 || got.Polls != 1 {
		t.Errorf("n1 presence after a poll that read no peers = %+v, want 1 of 1", got)
	}

	// Sources down: no status, so no chain to record on or read from.
	down := &node.Snapshot{Timestamp: seamNow, Peers: []node.Peer{{NodeID: "n1"}}}
	s.publish(t.Context(), down)
	if got := down.Peers[0].Presence; got != nil {
		t.Errorf("presence on a snapshot without status = %+v, want nil", got)
	}
	figures, err := s.Presence.Figures(t.Context(), "test-x", seamNow, []string{"n1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := figures["n1"]; got.Polls != 1 {
		t.Errorf("polls after the sources-down snapshot = %d, want 1", got.Polls)
	}
	if logs.Len() != 0 {
		t.Errorf("log = %q, want empty", logs.String())
	}
}

func TestPublishLogsAFailedPresence(t *testing.T) {
	s := newSeamServer(t)
	store, db := openPresenceDB(t)
	s.Presence = store
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	logs := captureLog(t)

	snap := presenceSnapshot(true)
	s.publish(t.Context(), snap)

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 2 ||
		!strings.Contains(lines[0], "presence: record the poll: begin tx: sql: database is closed") ||
		!strings.Contains(lines[1], "presence: read: begin tx: sql: database is closed") {
		t.Errorf("log = %q, want the record and the read failing on a closed database", logs.String())
	}
	for i, p := range snap.Peers {
		if p.Presence != nil {
			t.Errorf("peer %d presence = %+v after a failed read, want nil", i, p.Presence)
		}
	}
	if s.getSnapshot() != snap {
		t.Error("getSnapshot() is not the published snapshot")
	}
}
