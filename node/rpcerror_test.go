package node

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestIsRefusal(t *testing.T) {
	rpcErr := &RPCError{Code: -32603, Message: "Internal error", Data: "height 9 is not available"}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"JSON-RPC error", rpcErr, true},
		{"wrapped JSON-RPC error", fmt.Errorf("/blockchain: %w", rpcErr), true},
		{"HTTP error with a JSON-RPC body", &HTTPStatusError{StatusCode: http.StatusForbidden, RPC: rpcErr}, false},
		{"rate limited", &HTTPStatusError{StatusCode: http.StatusTooManyRequests}, false},
		{"transport error", errors.New("connection reset"), false},
		{"no error", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRefusal(tc.err); got != tc.want {
				t.Errorf("IsRefusal(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
