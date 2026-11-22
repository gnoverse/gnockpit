//go:build page

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/gnolang/gno/tm2/pkg/crypto"
)

// mechViewportWidth and mechViewportHeight return the window's inner size.
func mechViewportWidth(e *pageEnv) int  { return evalInt(e, `() => innerWidth`) }
func mechViewportHeight(e *pageEnv) int { return evalInt(e, `() => innerHeight`) }

// mechBlockTitle is the title of a recent block's popover: its height.
var mechBlockTitle = regexp.MustCompile(`^#\d+$`)

// mechAlphaOperator is the operator address of alpha's valopers profile.
var mechAlphaOperator = crypto.AddressFromPreimage([]byte("operator alpha")).String()

// mechDocsServer serves a page at every path, for the links a test follows, and
// returns its URL.
func mechDocsServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html><title>Docs</title>")
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
