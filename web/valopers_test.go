package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

func TestRefreshValopers(t *testing.T) {
	// One profile, whose state query the node refuses.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := base64.StdEncoding.DecodeString(strings.Trim(r.URL.Query().Get("data"), `"`))
		arg := string(raw)
		if strings.Contains(r.URL.Query().Get("path"), "qeval") {
			fmt.Fprint(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":{"@type":"/abci.StringError","value":"qeval refused"},"Data":null}}}}`)
			return
		}
		md := "no valopers"
		switch {
		case strings.HasSuffix(arg, "valopers:"):
			md = " * [Alice](/r/gnops/valopers:g1opa) - [profile](/r/demo/profile:u/g1opa)\n"
		case strings.HasSuffix(arg, ":g1opa"):
			md = "## Alice\nAlice's node.\n\n- Operator Address: g1opa\n- Signing Address: g1signa\n"
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, base64.StdEncoding.EncodeToString([]byte(md)))
	}))
	defer srv.Close()
	s := &Server{Client: node.NewClient(srv.URL, 5*time.Second)}
	s.Client.Names = node.NewNameRegistryWithPersist(filepath.Join(t.TempDir(), "names.json"))
	var logs bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&logs)
	s.refreshValopers(context.Background())
	log.SetOutput(orig)

	s.mu.RLock()
	alice, ok := s.valopers["g1opa"]
	s.mu.RUnlock()
	if !ok || alice.Moniker != "Alice" || alice.Description != "Alice's node." || alice.State != nil {
		t.Errorf("valopers[g1opa] = %+v, %v; want Alice's profile without state", alice, ok)
	}
	if got := s.Client.Names.Name("g1signa"); got != "Alice" {
		t.Errorf("name of g1signa = %q, want Alice", got)
	}
	if got := logs.String(); !strings.Contains(got, "valoper state unavailable for 1 of 1: ") || !strings.Contains(got, "qeval refused") || strings.Count(got, "\n") != 1 {
		t.Errorf("logs = %q, want one line naming 1 of 1 and the node's reason", got)
	}
}
