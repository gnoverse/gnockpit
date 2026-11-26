package web

import (
	"net/http"

	"github.com/gnoverse/gnockpit/ranking"
)

// rankingAnswer is /api/ranking's payload: the ranking's state, whether
// readers hide the excluded operators, and the latest day published, null
// while none is.
type rankingAnswer struct {
	State        ranking.State      `json:"state"`
	HideExcluded bool               `json:"hide_excluded"`
	Ranking      *ranking.Published `json:"ranking"`
}

// handleRanking serves the ranking's state and latest published day; 404
// while the ranking is off.
func (s *Server) handleRanking(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		serveJSON(w, "/api/ranking", http.StatusMethodNotAllowed, errorBody{Error: "method not allowed"})
		return
	}
	if s.Ranking == nil {
		serveJSON(w, "/api/ranking", http.StatusNotFound, errorBody{Error: "ranking disabled"})
		return
	}
	snap := s.Ranking.Snapshot()
	serveJSON(w, "/api/ranking", http.StatusOK, rankingAnswer{State: snap.State, HideExcluded: s.RankingHideExcluded, Ranking: snap.Published})
}
