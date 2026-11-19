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
	"time"

	"github.com/gnoverse/gnockpit/blocks"
)

// maxHistoryBlocks bounds the heights one /api/history call returns.
const maxHistoryBlocks = 1000

// maxHistoryDays bounds the days of vote delays one /api/history call returns.
const maxHistoryDays = 31

// historyTime formats /api/history's times: RFC 3339 UTC, to the millisecond.
const historyTime = "2006-01-02T15:04:05.000Z07:00"

// historyReport is the /api/history payload: how far the block history
// reaches; for ?from=&to=, the blocks of that range it holds, with the
// validator sets they name; for ?days=, the vote delays of those UTC days.
// Blocks and Valsets are nil without a range, and empty for a range it holds
// none of.
type historyReport struct {
	Chain      string                     `json:"chain"`
	Filled     historyFilled              `json:"filled"`
	Blocks     []historyBlock             `json:"blocks,omitzero"`
	Valsets    map[string][]historyMember `json:"valsets,omitzero"`
	VoteDelays *historyDelays             `json:"vote_delays,omitempty"`
}

type historyFilled struct {
	From     int64   `json:"from"`
	FromTime string  `json:"from_time,omitempty"`
	To       int64   `json:"to"`
	Floor    int64   `json:"floor,omitempty"`
	Complete bool    `json:"complete"`
	Rate     float64 `json:"rate,omitempty"` // heights added below From per second while filling
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
	// Rounds is the expected proposer of rounds 0 to Round, "" when unknown,
	// for a block finalized after round 0.
	Rounds    []string          `json:"rounds,omitempty"`
	Flag      string            `json:"flag,omitempty"`       // why the block's proposer check failed
	SetChange *historySetChange `json:"set_change,omitempty"` // the events behind the validator set the block opens
}

type historySetChange struct {
	Source int64          `json:"source"` // the block the events were read from
	Events []historyEvent `json:"events"`
}

type historyEvent struct {
	Type       string `json:"type"`
	Operator   string `json:"operator"`
	Address    string `json:"address,omitempty"`
	OldAddress string `json:"old_address,omitempty"`
	NewAddress string `json:"new_address,omitempty"`
	Power      int64  `json:"power,omitempty"`
	Applied    bool   `json:"applied"`
}

type historyMember struct {
	Address string `json:"address"`
	PubKey  string `json:"pub_key,omitempty"`
	Power   int64  `json:"power"`
}

type historyDelays struct {
	EdgesMs []int64           `json:"edges_ms"` // bucket i holds the delays between edges i-1 and i; an edge falls in the bucket farther from 0
	Days    []historyDelayDay `json:"days"`
}

type historyDelayDay struct {
	Day        string               `json:"day"` // YYYY-MM-DD, UTC
	Validators []historyDelayCounts `json:"validators"`
}

type historyDelayCounts struct {
	Address string  `json:"address"`
	Votes   int64   `json:"votes"`
	MeanMs  int64   `json:"mean_ms"`
	Buckets []int64 `json:"buckets"`
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
	rep := historyReport{Chain: p.ChainID, Filled: historyFilled{From: p.From, To: p.To, Floor: p.Floor, Complete: p.Complete, Rate: p.Rate}}
	if !p.FromTime.IsZero() {
		rep.Filled.FromTime = p.FromTime.UTC().Format(historyTime)
	}
	if q.Has("days") {
		days, err := strconv.Atoi(q.Get("days"))
		if err != nil || days < 1 || days > maxHistoryDays {
			return historyReport{}, http.StatusBadRequest, fmt.Errorf("days must be from 1 to %d", maxHistoryDays)
		}
		if rep.VoteDelays, err = s.historyDelays(ctx, days); err != nil {
			log.Printf("history: vote delays of %d days: %v", days, err)
			return historyReport{}, http.StatusInternalServerError, errors.New("internal error")
		}
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
	span, err := s.Blocks.Blocks(ctx, from, to)
	if err != nil {
		log.Printf("history: blocks %d-%d: %v", from, to, err)
		return historyReport{}, http.StatusInternalServerError, errors.New("internal error")
	}
	rep.Blocks = make([]historyBlock, 0, len(span.Blocks))
	for _, r := range span.Blocks {
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
		b.Rounds, b.Flag = r.Rounds, r.Flag
		if change, ok := span.Changes[r.Height]; ok {
			b.SetChange = &historySetChange{Source: change.Source, Events: make([]historyEvent, len(change.Events))}
			for i, e := range change.Events {
				b.SetChange.Events[i] = historyEvent{
					Type: e.Type, Operator: e.Operator, Address: e.Address,
					OldAddress: e.OldAddress, NewAddress: e.NewAddress, Power: e.Power, Applied: e.Applied,
				}
			}
		}
		rep.Blocks = append(rep.Blocks, b)
	}
	rep.Valsets = make(map[string][]historyMember, len(span.Sets))
	for hash, members := range span.Sets {
		out := make([]historyMember, len(members))
		for i, m := range members {
			out[i] = historyMember{Address: m.Address, PubKey: m.PubKey, Power: m.Power}
		}
		rep.Valsets[hash] = out
	}
	return rep, http.StatusOK, nil
}

// historyDelays returns the vote delays of the last days UTC days, today
// included.
func (s *Server) historyDelays(ctx context.Context, days int) (*historyDelays, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	got, err := s.Blocks.VoteDelays(ctx, today.AddDate(0, 0, 1-days), today)
	if err != nil {
		return nil, err
	}
	edges := blocks.DelayEdges()
	out := &historyDelays{EdgesMs: edges[:], Days: []historyDelayDay{}}
	for _, d := range got {
		day := d.Day.Format(time.DateOnly)
		if n := len(out.Days); n == 0 || out.Days[n-1].Day != day {
			out.Days = append(out.Days, historyDelayDay{Day: day})
		}
		last := &out.Days[len(out.Days)-1]
		last.Validators = append(last.Validators, historyDelayCounts{
			Address: d.Address, Votes: d.Votes, MeanMs: d.SumMs / max(d.Votes, 1), Buckets: d.Buckets[:],
		})
	}
	return out, nil
}
