package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// HTTPStatusError is a non-2xx answer from an RPC endpoint. RetryAfter is the
// wait the endpoint asked for in its Retry-After header, zero when absent. RPC
// holds the JSON-RPC error the body carried, if any (gno answers a refused
// query with HTTP 403 and the reason in a JSON-RPC error).
type HTTPStatusError struct {
	URL        string
	StatusCode int
	RetryAfter time.Duration
	RPC        *RPCError
}

func newHTTPStatusError(url string, resp *http.Response, body []byte) *HTTPStatusError {
	e := &HTTPStatusError{URL: url, StatusCode: resp.StatusCode}
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
		e.RetryAfter = time.Duration(secs) * time.Second
	}
	var envelope rpcResult
	if json.Unmarshal(body, &envelope) == nil {
		e.RPC = envelope.Error
	}
	return e
}

func (e *HTTPStatusError) Error() string {
	msg := fmt.Sprintf("GET %s: HTTP %d %s", e.URL, e.StatusCode, http.StatusText(e.StatusCode))
	if e.RPC != nil {
		msg += ": " + e.RPC.Error()
	}
	return msg
}

// RPCError is the error member of a JSON-RPC response: the node answered but
// could not serve the call. gno sends it with HTTP 200 for most failures, such
// as a height out of range.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func (e *RPCError) Error() string {
	if e.Data == "" {
		return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("rpc error %d: %s: %s", e.Code, e.Message, e.Data)
}

// IsRefusal reports whether err is the node answering that it cannot serve a
// call (a JSON-RPC error with HTTP 2xx), as opposed to failing to answer it:
// a transport error or an HTTP error status, which may pass on a retry.
func IsRefusal(err error) bool {
	var re *RPCError
	return errors.As(err, &re)
}
