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
	"strconv"
	"strings"
	"sync"
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

func TestRefreshValopersKeepsAProfileItCouldNotRead(t *testing.T) {
	// Alice's render fails on the second read.
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := base64.StdEncoding.DecodeString(strings.Trim(r.URL.Query().Get("data"), `"`))
		arg := string(raw)
		if strings.Contains(r.URL.Query().Get("path"), "qeval") {
			ans := "(true bool)"
			if strings.HasSuffix(arg, ".LastRotationHeight") {
				ans = "(5 int64)"
			}
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, base64.StdEncoding.EncodeToString([]byte(ans)))
			return
		}
		md := "no valopers"
		switch {
		case strings.HasSuffix(arg, "valopers:"):
			md = " * [Alice](/r/gnops/valopers:g1opa) - [profile](/r/demo/profile:u/g1opa)\n"
		case strings.HasSuffix(arg, ":g1opa"):
			if reads++; reads > 1 {
				fmt.Fprint(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":{"@type":"/abci.StringError","value":"render failed"},"Data":null}}}}`)
				return
			}
			md = "## Alice\nAlice's node.\n\n- Operator Address: g1opa\n- Signing Address: g1signa\n"
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, base64.StdEncoding.EncodeToString([]byte(md)))
	}))
	defer srv.Close()
	s := &Server{Client: node.NewClient(srv.URL, 5*time.Second)}
	s.Client.Names = node.NewNameRegistryWithPersist(filepath.Join(t.TempDir(), "names.json"))
	s.refreshValopers(context.Background())
	var logs bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&logs)
	s.refreshValopers(context.Background())
	log.SetOutput(orig)

	s.mu.RLock()
	alice, ok := s.valopers["g1opa"]
	s.mu.RUnlock()
	if !ok || alice.Moniker != "Alice" {
		t.Errorf("valopers[g1opa] = %+v, %v; want the profile of the read before", alice, ok)
	}
	if _, whole := s.Valopers(); !whole {
		t.Errorf("Valopers() whole = false, want true: the one listed operator has its last profile")
	}
	if got := logs.String(); !strings.Contains(got, "valoper profile unread for 1 of 1, the last read kept where there is one: g1opa: ") || !strings.Contains(got, "render failed") || strings.Count(got, "\n") != 1 {
		t.Errorf("logs = %q, want one line naming 1 of 1, g1opa and the node's reason", got)
	}
}

// valopersNode is a node answering the valopers realm: its list, one page
// per entry of pages, then a page listing none; each listed operator's
// profile (moniker "Name of <op>", signing key "<op>sign") and its state. A
// list page in failPage, and the profile of an operator in failProfile, fail.
type valopersNode struct {
	mu          sync.Mutex
	pages       [][]string
	failPage    map[int]bool
	failProfile map[string]bool
}

// set changes what the node fails, between two refreshes.
func (n *valopersNode) set(change func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	change()
}

func (n *valopersNode) serve(t *testing.T) *httptest.Server {
	t.Helper()
	answer := func(w http.ResponseWriter, data string) {
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, base64.StdEncoding.EncodeToString([]byte(data)))
	}
	refuse := func(w http.ResponseWriter, why string) {
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":{"@type":"/abci.StringError","value":%q},"Data":null}}}}`, why)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		defer n.mu.Unlock()
		raw, _ := base64.StdEncoding.DecodeString(strings.Trim(r.URL.Query().Get("data"), `"`))
		arg := string(raw)
		if strings.Contains(r.URL.Query().Get("path"), "qeval") {
			if strings.HasSuffix(arg, ".LastRotationHeight") {
				answer(w, "(5 int64)")
				return
			}
			answer(w, "(true bool)")
			return
		}
		_, path, _ := strings.Cut(arg, "valopers:")
		page := 0
		switch {
		case path == "":
			page = 1
		case strings.HasPrefix(path, "?page="):
			page, _ = strconv.Atoi(strings.TrimPrefix(path, "?page="))
		}
		if page > 0 {
			if n.failPage[page] {
				refuse(w, fmt.Sprintf("page %d render failed", page))
				return
			}
			var md strings.Builder
			if page <= len(n.pages) {
				for _, op := range n.pages[page-1] {
					fmt.Fprintf(&md, " * [Name of %s](/r/gnops/valopers:%s) - [profile](/r/demo/profile:u/%s)\n", op, op, op)
				}
			}
			answer(w, md.String())
			return
		}
		if n.failProfile[path] {
			refuse(w, "profile render failed")
			return
		}
		answer(w, fmt.Sprintf("## Name of %s\n- Operator Address: %s\n- Signing Address: %ssign\n", path, path, path))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// valopersServer returns a server reading the valopers realm from n.
func valopersServer(t *testing.T, n *valopersNode) *Server {
	t.Helper()
	s := &Server{Client: node.NewClient(n.serve(t).URL, 5*time.Second)}
	s.Client.Names = node.NewNameRegistryWithPersist(filepath.Join(t.TempDir(), "names.json"))
	return s
}

// logsOf returns what f logs.
func logsOf(f func()) string {
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prev)
	f()
	return logs.String()
}

func TestValopersWaitForEveryListedProfile(t *testing.T) {
	n := &valopersNode{pages: [][]string{{"g1opa", "g1opb"}}, failProfile: map[string]bool{"g1opb": true}}
	s := valopersServer(t, n)
	logs := logsOf(func() { s.refreshValopers(context.Background()) })
	if got, ok := s.Valopers(); ok || got["g1opa"].Moniker != "Name of g1opa" {
		t.Errorf("Valopers() after a read missing g1opb, never read = %v, %v; want g1opa's profile, false", got, ok)
	}
	if !strings.Contains(logs, "g1opb: ") || !strings.Contains(logs, "profile render failed") {
		t.Errorf("logs = %q, want g1opb named with the node's reason", logs)
	}
	n.set(func() { n.failProfile = nil })
	s.refreshValopers(context.Background())
	if got, ok := s.Valopers(); !ok || len(got) != 2 {
		t.Errorf("Valopers() after a whole read = %v, %v; want both profiles, true", got, ok)
	}
	n.set(func() { n.failProfile = map[string]bool{"g1opb": true} })
	logsOf(func() { s.refreshValopers(context.Background()) })
	if got, ok := s.Valopers(); !ok || got["g1opb"].Moniker != "Name of g1opb" {
		t.Errorf("Valopers() after a read missing g1opb, read before = %v, %v; want its last profile, true", got, ok)
	}
}

func TestRefreshValopersKeepsEveryProfileOnAPartialList(t *testing.T) {
	n := &valopersNode{pages: [][]string{{"g1opa"}, {"g1opb"}}}
	s := valopersServer(t, n)
	s.refreshValopers(context.Background())
	if got, ok := s.Valopers(); !ok || len(got) != 2 {
		t.Fatalf("Valopers() after a whole read = %v, %v; want both profiles, true", got, ok)
	}
	n.set(func() { n.failPage = map[int]bool{2: true} })
	logs := logsOf(func() { s.refreshValopers(context.Background()) })
	if got, ok := s.Valopers(); ok || got["g1opb"].Moniker != "Name of g1opb" || got["g1opa"].Moniker != "Name of g1opa" {
		t.Errorf("Valopers() after a list read in part = %v, %v; want g1opa's and g1opb's last profile, false", got, ok)
	}
	if !strings.Contains(logs, "page 2 render failed") {
		t.Errorf("logs = %q, want the list page's failure", logs)
	}
	n.set(func() { n.failPage = map[int]bool{1: true} })
	logsOf(func() { s.refreshValopers(context.Background()) })
	if got, ok := s.Valopers(); ok || len(got) != 2 {
		t.Errorf("Valopers() after a failed read = %v, %v; want both last profiles, false", got, ok)
	}
}
