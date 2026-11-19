package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gnoverse/gnockpit/blocks"
)

// maxHistoryBlocks bounds the heights one /api/history call returns.
const maxHistoryBlocks = 1000

// historyTime formats /api/history's times: RFC 3339 UTC, to the millisecond.
const historyTime = "2006-01-02T15:04:05.000Z07:00"

// historyReport is the /api/history payload: how far the block history
// reaches and, for ?from=&to=, the blocks of that range it holds, with the
// validator sets they name. Blocks and Valsets are nil without a range, and
// empty for a range it holds none of.
type historyReport struct {
	Chain   string                     `json:"chain"`
	Filled  historyFilled              `json:"filled"`
	Blocks  []historyBlock             `json:"blocks,omitzero"`
	Valsets map[string][]historyMember `json:"valsets,omitzero"`
}

type historyFilled struct {
	From     int64  `json:"from"`
	FromTime string `json:"from_time,omitempty"`
	To       int64  `json:"to"`
	Floor    int64  `json:"floor,omitempty"`
	Complete bool   `json:"complete"`
}

type historyBlock struct {
	Height     int64    `json:"height"`
	Time       string   `json:"time"`
	Proposer   string   `json:"proposer"`
	Round      int      `json:"round"`
	Txs        int      `json:"txs"`
	Valset     string   `json:"valset"`
	Absent     []string `json:"absent"`
	Nil        []string `json:"nil"`
	OtherBlock []string `json:"other_block"`
}

type historyMember struct {
	Address string `json:"address"`
	Power   int64  `json:"power"`
}

type historyErrorBody struct {
	Error string `json:"error"`
}

// handleHistory serves the block history as JSON, CORS-open.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	rep, status, err := s.historyReport(r.Context(), r.URL.Query())
	var payload any = rep
	if err != nil {
		payload = historyErrorBody{Error: err.Error()}
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		log.Printf("history: encode: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	w.Write(body)
}

func (s *Server) historyReport(ctx context.Context, q url.Values) (historyReport, int, error) {
	if s.Blocks == nil || s.BlockFill == nil {
		return historyReport{}, http.StatusServiceUnavailable, errors.New("block history disabled")
	}
	p := s.BlockFill.Progress()
	rep := historyReport{Chain: p.ChainID, Filled: historyFilled{From: p.From, To: p.To, Floor: p.Floor, Complete: p.Complete}}
	if !p.FromTime.IsZero() {
		rep.Filled.FromTime = p.FromTime.UTC().Format(historyTime)
	}
	fromS, toS := q.Get("from"), q.Get("to")
	if fromS == "" && toS == "" {
		return rep, http.StatusOK, nil
	}
	from, err1 := strconv.ParseInt(fromS, 10, 64)
	to, err2 := strconv.ParseInt(toS, 10, 64)
	switch {
	case err1 != nil || err2 != nil:
		return historyReport{}, http.StatusBadRequest, errors.New("from and to must both be block heights")
	case from < 1 || to < from:
		return historyReport{}, http.StatusBadRequest, errors.New("from must be at least 1 and at most to")
	case to-from+1 > maxHistoryBlocks:
		return historyReport{}, http.StatusBadRequest, fmt.Errorf("at most %d blocks per call", maxHistoryBlocks)
	}
	recs, sets, err := s.Blocks.Blocks(ctx, from, to)
	if err != nil {
		log.Printf("history: blocks %d-%d: %v", from, to, err)
		return historyReport{}, http.StatusInternalServerError, errors.New("internal error")
	}
	rep.Blocks = make([]historyBlock, 0, len(recs))
	for _, r := range recs {
		b := historyBlock{
			Height: r.Height, Time: r.Time.UTC().Format(historyTime), Proposer: r.Proposer, Round: r.Round, Txs: r.Txs,
			Valset: r.ValsetHash, Absent: []string{}, Nil: []string{}, OtherBlock: []string{},
		}
		for _, a := range r.Absences {
			switch a.Kind {
			case blocks.Absent:
				b.Absent = append(b.Absent, a.Address)
			case blocks.Nil:
				b.Nil = append(b.Nil, a.Address)
			case blocks.OtherBlock:
				b.OtherBlock = append(b.OtherBlock, a.Address)
			}
		}
		rep.Blocks = append(rep.Blocks, b)
	}
	rep.Valsets = make(map[string][]historyMember, len(sets))
	for hash, members := range sets {
		out := make([]historyMember, len(members))
		for i, m := range members {
			out[i] = historyMember{Address: m.Address, Power: m.Power}
		}
		rep.Valsets[hash] = out
	}
	return rep, http.StatusOK, nil
}
