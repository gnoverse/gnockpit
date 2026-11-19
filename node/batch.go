package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// rpcCall is one call of a JSON-RPC batch.
type rpcCall struct {
	Method string
	Params map[string]string
}

// HeightError is the failure of the call for one height within a batch.
type HeightError struct {
	Height int64
	Err    error
}

func (e *HeightError) Error() string { return fmt.Sprintf("height %d: %v", e.Height, e.Err) }

func (e *HeightError) Unwrap() error { return e.Err }

// rpcBatch posts calls as one JSON-RPC batch and returns each call's answer, in
// call order. A batch the node refuses whole (one error object instead of an
// array, as tm2 answers a batch above its max_batch_requests) returns that
// *RPCError, wrapped.
func (c *Client) rpcBatch(ctx context.Context, calls []rpcCall) ([]rpcResult, error) {
	type request struct {
		JSONRPC string            `json:"jsonrpc"`
		ID      int               `json:"id"`
		Method  string            `json:"method"`
		Params  map[string]string `json:"params"`
	}
	reqs := make([]request, len(calls))
	for i, call := range calls {
		reqs[i] = request{JSONRPC: "2.0", ID: i, Method: call.Method, Params: call.Params}
	}
	payload, err := json.Marshal(reqs)
	if err != nil {
		return nil, fmt.Errorf("encode batch: %w", err)
	}
	body, err := c.send(ctx, http.MethodPost, c.RPCURL, payload)
	if err != nil {
		return nil, err
	}
	var answers []struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(body, &answers); err != nil {
		var whole rpcResult
		if json.Unmarshal(body, &whole) == nil && whole.Error != nil {
			return nil, fmt.Errorf("batch of %d calls: %w", len(calls), whole.Error)
		}
		return nil, fmt.Errorf("unmarshal batch: %w (body: %s)", err, truncate(string(body), 200))
	}
	out := make([]rpcResult, len(calls))
	seen := make([]bool, len(calls))
	for _, a := range answers {
		if a.ID < 0 || a.ID >= len(calls) || seen[a.ID] {
			return nil, fmt.Errorf("batch answer with unexpected id %d", a.ID)
		}
		seen[a.ID] = true
		out[a.ID] = rpcResult{Result: a.Result, Error: a.Error}
	}
	for i, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("batch answer missing call %d", i)
		}
	}
	return out, nil
}
