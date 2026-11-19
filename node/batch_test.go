package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// batchRequest is one call as the node receives it in a JSON-RPC batch.
type batchRequest struct {
	ID     int               `json:"id"`
	Method string            `json:"method"`
	Params map[string]string `json:"params"`
}

// batchServer answers each batch with the body answer builds from its calls.
func batchServer(t *testing.T, answer func([]batchRequest) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var reqs []batchRequest
		if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
			t.Errorf("decode batch: %v", err)
			return
		}
		fmt.Fprint(w, answer(reqs))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// commitAnswer answers call id with the commit of height: its slots are
// signed for the block by g1a, empty, nil by g1c, and for another block by g1d.
func commitAnswer(id int, height string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"canonical":true,"signed_header":{
		"header":{"height":%q,"time":"2026-11-19T02:29:47.468Z","proposer_address":"g1prop","num_txs":"3","validators_hash":"SETHASH"},
		"commit":{"block_id":{"hash":"BLOCK%s"},"precommits":[
			{"round":"1","block_id":{"hash":"BLOCK%s"},"timestamp":"2026-11-19T02:29:48Z","validator_address":"g1a","validator_index":"0"},
			null,
			{"round":"1","block_id":{"hash":""},"timestamp":"2026-11-19T02:29:49Z","validator_address":"g1c","validator_index":"2"},
			{"round":"1","block_id":{"hash":"OTHER"},"timestamp":"2026-11-19T02:29:50Z","validator_address":"g1d","validator_index":"3"}
		]}
	}}}`, id, height, height, height)
}

func TestCommits(t *testing.T) {
	srv := batchServer(t, func(reqs []batchRequest) string {
		// Answered in reverse: results are matched to calls by id.
		parts := make([]string, len(reqs))
		for i, r := range reqs {
			if r.Method != "commit" {
				t.Errorf("method = %q, want commit", r.Method)
			}
			parts[len(reqs)-1-i] = commitAnswer(r.ID, r.Params["height"])
		}
		return "[" + strings.Join(parts, ",") + "]"
	})
	got, err := NewClient(srv.URL, 5*time.Second).Commits(context.Background(), []int64{10, 11})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Height != 10 || got[1].Height != 11 {
		t.Fatalf("Commits() = %+v, want heights 10 then 11", got)
	}
	c := got[0]
	if c.Proposer != "g1prop" || c.NumTxs != 3 || c.ValidatorsHash != "SETHASH" || c.BlockHash != "BLOCK10" || !c.Canonical {
		t.Errorf("commit = %+v", c)
	}
	if want := time.Date(2026, 11, 19, 2, 29, 47, 468_000_000, time.UTC); !c.Time.Equal(want) {
		t.Errorf("time = %v, want %v", c.Time, want)
	}
	if len(c.Precommits) != 4 || c.Precommits[1] != nil {
		t.Fatalf("precommits = %+v, want 4 with the second empty", c.Precommits)
	}
	signed, nilVote, other := c.Precommits[0], c.Precommits[2], c.Precommits[3]
	if signed.Address != "g1a" || signed.Index != 0 || signed.Round != 1 || signed.BlockHash != "BLOCK10" ||
		!signed.Time.Equal(time.Date(2026, 11, 19, 2, 29, 48, 0, time.UTC)) {
		t.Errorf("signed precommit = %+v", signed)
	}
	if nilVote.BlockHash != "" || nilVote.Index != 2 {
		t.Errorf("nil precommit = %+v", nilVote)
	}
	if other.BlockHash != "OTHER" || other.Address != "g1d" {
		t.Errorf("other-block precommit = %+v", other)
	}
}

func TestCommitsRefusedHeight(t *testing.T) {
	srv := batchServer(t, func([]batchRequest) string {
		return "[" + commitAnswer(0, "10") +
			`,{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"Internal error","data":"height 11 must be less than or equal to the current blockchain height 10"}}]`
	})
	got, err := NewClient(srv.URL, 5*time.Second).Commits(context.Background(), []int64{10, 11})
	if len(got) != 1 || got[0].Height != 10 {
		t.Errorf("got %d commits, want the one of height 10", len(got))
	}
	var he *HeightError
	if !errors.As(err, &he) || he.Height != 11 {
		t.Fatalf("err = %v, want a *HeightError for 11", err)
	}
	if !IsRefusal(err) {
		t.Errorf("IsRefusal(%v) = false, want true", err)
	}
}

func TestCommitsBatchRefusedWhole(t *testing.T) {
	srv := batchServer(t, func([]batchRequest) string {
		return `{"jsonrpc":"2.0","id":"","error":{"code":-32600,"message":"Invalid Request","data":"batch of 2 requests exceeds the maximum of 1"}}`
	})
	got, err := NewClient(srv.URL, 5*time.Second).Commits(context.Background(), []int64{10, 11})
	if err == nil || len(got) != 0 {
		t.Fatalf("Commits() = %d commits, %v; want none and an error", len(got), err)
	}
	var he *HeightError
	if errors.As(err, &he) {
		t.Errorf("err = %v, want no *HeightError for a batch refused whole", err)
	}
	if !IsRefusal(err) {
		t.Errorf("IsRefusal(%v) = false, want true", err)
	}
}

func TestCommitsRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, 5*time.Second).Commits(context.Background(), []int64{10})
	var hs *HTTPStatusError
	if !errors.As(err, &hs) || hs.StatusCode != http.StatusTooManyRequests || hs.RetryAfter != 7*time.Second {
		t.Fatalf("err = %v, want an HTTP 429 asking for 7s", err)
	}
	if !strings.HasPrefix(err.Error(), "POST ") {
		t.Errorf("error = %q, want it to name the POST", err)
	}
}
