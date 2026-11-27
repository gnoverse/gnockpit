package web

import (
	"context"
	"log"

	"github.com/gnoverse/gnockpit/node"
)

// refreshPresence records the poll snap came from, when a source's /net_info
// answered, and sets each peer's Presence from the polls stored. Without a
// store or a Status (no chain to key on) it does nothing. A failure is logged;
// after a failed read the peers keep no presence.
func (s *Server) refreshPresence(ctx context.Context, snap *node.Snapshot) {
	if s.Presence == nil || snap.Status == nil {
		return
	}
	chain := snap.Status.NodeInfo.Network
	ids := make([]string, 0, len(snap.Peers))
	for _, p := range snap.Peers {
		if p.NodeID != "" {
			ids = append(ids, p.NodeID)
		}
	}
	if snap.PeersRead {
		if err := s.Presence.Record(ctx, chain, snap.Timestamp, ids); err != nil {
			log.Printf("presence: record the poll: %v", err)
		}
	}
	figures, err := s.Presence.Figures(ctx, chain, snap.Timestamp, ids)
	if err != nil {
		log.Printf("presence: read: %v", err)
		return
	}
	for i := range snap.Peers {
		if f, ok := figures[snap.Peers[i].NodeID]; ok {
			snap.Peers[i].Presence = &f
		}
	}
}
