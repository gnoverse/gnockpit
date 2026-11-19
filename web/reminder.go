package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gnoverse/gnockpit/eta"
	"github.com/gnoverse/gnockpit/web/push"
)

// reminderRequest is the payload of PUT /api/push/reminder: the device's
// push subscription and its ETA card.
type reminderRequest struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
	Height   int64  `json:"height"`
	Since    string `json:"since"` // the card's since, RFC 3339
	Label    string `json:"label"`
	Lead     string `json:"lead"` // as push.ParseLead reads it
}

// reminderView is a device's reminder, as GET /api/push/reminder serves it.
type reminderView struct {
	Height int64  `json:"height"`
	Since  string `json:"since"`
	Label  string `json:"label"`
	Lead   string `json:"lead"`
}

// handlePushReminder serves a device's reminder before its ETA card's
// height: GET ?id= reads it (204 when there is none), PUT sets it (replacing the device's reminder),
// DELETE ?id= removes it, and the device with it when it has no alert left.
func (s *Server) handlePushReminder(w http.ResponseWriter, r *http.Request) {
	if s.PushManager == nil {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	db := s.PushManager.DB()
	id := r.URL.Query().Get("id")
	switch r.Method {
	case http.MethodGet:
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		rem, ok, err := db.Reminder(id)
		if err != nil {
			log.Printf("push: %v", err)
			http.Error(w, "read reminder", http.StatusInternalServerError)
			return
		}
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, reminderView{Height: rem.Height, Since: rem.Since, Label: rem.Label, Lead: rem.Lead.String()})
	case http.MethodPut:
		s.putReminder(w, r)
	case http.MethodDelete:
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		kept, err := db.DeleteReminder(id)
		if err != nil {
			log.Printf("push: %v", err)
			http.Error(w, "delete reminder", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"device_kept": kept})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) putReminder(w http.ResponseWriter, r *http.Request) {
	var req reminderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	lead, err := push.ParseLead(req.Lead)
	switch {
	case req.Endpoint == "" || req.P256dh == "" || req.Auth == "":
		err = fmt.Errorf("endpoint, p256dh and auth are required")
	case req.Height < 1:
		err = fmt.Errorf("height must be a block height")
	case err == nil:
		if _, perr := time.Parse(time.RFC3339, req.Since); perr != nil {
			err = fmt.Errorf("since must be an RFC 3339 time")
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sched, ok := s.schedule()
	if !ok {
		http.Error(w, "no chain status yet", http.StatusServiceUnavailable)
		return
	}
	if lead.Met(req.Height, sched.Head, sched.TimeLeft) {
		http.Error(w, fmt.Sprintf("#%d is less than %s away", req.Height, lead), http.StatusConflict)
		return
	}
	sub := push.Subscription{ID: generateID(), Endpoint: req.Endpoint, P256dh: req.P256dh, Auth: req.Auth}
	id, err := s.PushManager.DB().SetReminder(sub, push.Reminder{Chain: sched.Chain, Height: req.Height, Since: req.Since, Label: req.Label, Lead: lead})
	if err != nil {
		log.Printf("push: %v", err)
		http.Error(w, "save reminder", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

// notifySchedule sends the halt alerts and reminders the chain's state calls
// for, when push is on and a snapshot is in.
func (s *Server) notifySchedule() {
	if s.PushManager == nil {
		return
	}
	if sched, ok := s.schedule(); ok {
		s.PushManager.EvaluateSchedule(sched)
	}
}

// schedule is what the lead-time alerts are checked against: the newest
// block, and the block-time model's scheduled halt and estimates. The time
// left to a height runs from the newest block's time at the model's pace, so
// a stalled chain does not meet every lead in time at once. It is not ok
// before the first snapshot.
func (s *Server) schedule() (push.Schedule, bool) {
	snap := s.getSnapshot()
	if snap == nil || snap.Status == nil {
		return push.Schedule{}, false
	}
	head, err := snap.Status.Head()
	if err != nil {
		return push.Schedule{}, false
	}
	sched := push.Schedule{Chain: head.ChainID, Head: head.Height, TimeLeft: func(int64) (time.Duration, bool) { return 0, false }}
	if s.ETA == nil {
		return sched, true
	}
	v := s.ETA.View()
	if v.Halt != nil {
		sched.Halt = &push.Halt{Height: v.Halt.Height, MinVersion: v.Halt.MinVersion}
	}
	if v.Model != nil {
		tip := eta.Sample{Height: head.Height, Time: head.Time}
		sched.TimeLeft = func(h int64) (time.Duration, bool) {
			e, ok := v.Model.Estimate(tip, h)
			return e.ETA.Sub(tip.Time), ok
		}
	}
	return sched, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
