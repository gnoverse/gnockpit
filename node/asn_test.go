package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProvider(t *testing.T) {
	tests := []struct {
		org  string
		want string
	}{
		{"Amazon.com, Inc.", "AWS"},
		{"Amazon Data Services", "AWS"},
		{"Google LLC", "GCP"},
		{"Microsoft Corporation", "Azure"},
		{"Hetzner Online GmbH", "Hetzner"},
		{"OVH SAS", "OVH"},
		{"DigitalOcean, LLC", "DigitalOcean"},
		{"Contabo GmbH", "Contabo"},
		{"Akamai Connected Cloud", "Linode"},
		{"Vultr Holdings, LLC", "Vultr"},
		{"Oracle Corporation", "Oracle"},
		{"Alibaba (US) Technology Co., Ltd.", "Alibaba"},
		{"Some Local ISP Ltd", "Some Local ISP Ltd"}, // unknown -> raw org
		{"", ""},
	}
	for _, tc := range tests {
		if got := Provider(tc.org); got != tc.want {
			t.Errorf("Provider(%q) = %q, want %q", tc.org, got, tc.want)
		}
	}
}

// The download/refresh tests exercise ASN's mmdb machinery (download, month
// caching, previous-month fallback, and the concurrent reader-swap). The served
// bytes are a valid mmdb fixture; ASN-record decoding is covered separately by
// the live database, so these assert the machinery, not lookup content.

func TestASNEnsureFreshDownloadsAndCaches(t *testing.T) {
	body := gzipFixture(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dbip-asn-lite-2026-06.mmdb.gz" {
			atomic.AddInt32(&hits, 1)
			w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	a := NewASN(filepath.Join(t.TempDir(), "asn.mmdb"), nil)
	a.baseURL = srv.URL

	if err := a.EnsureFresh(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if a.loadedMonth != "2026-06" {
		t.Errorf("loadedMonth = %q, want 2026-06", a.loadedMonth)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("hits = %d, want 1", n)
	}
	// Same month again: must not re-download.
	if err := a.EnsureFresh(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("re-downloaded within the same month: hits = %d, want 1", n)
	}
}

func TestASNEnsureFreshFallsBackToPreviousMonth(t *testing.T) {
	body := gzipFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Current month not yet published; previous month is available.
		if r.URL.Path == "/dbip-asn-lite-2026-05.mmdb.gz" {
			w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	a := NewASN(filepath.Join(t.TempDir(), "asn.mmdb"), nil)
	a.baseURL = srv.URL

	if err := a.EnsureFresh(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if a.loadedMonth != "2026-05" {
		t.Errorf("loadedMonth = %q, want 2026-05 (fallback)", a.loadedMonth)
	}
}

// TestASNConcurrentLookupAndRefresh exercises the reader-lifecycle hazard: many
// lookups running while the database is repeatedly re-downloaded (each swap
// closing/unmapping the previous reader). Run with -race.
func TestASNConcurrentLookupAndRefresh(t *testing.T) {
	body := gzipFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	a := NewASN(filepath.Join(t.TempDir(), "asn.mmdb"), nil)
	a.baseURL = srv.URL
	if err := a.download(context.Background(), "2026-06"); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 40; i++ {
			if err := a.download(context.Background(), "2026-06"); err != nil {
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					a.Lookup("1.1.1.1")
				}
			}
		}()
	}
	wg.Wait()
}
