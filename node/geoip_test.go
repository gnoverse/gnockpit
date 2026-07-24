package node

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testMMDB = "testdata/dbip-city-test.mmdb"

func gzipFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(testMMDB)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		t.Fatal(err)
	}
	gz.Close()
	return buf.Bytes()
}

func TestGeoIPLookup(t *testing.T) {
	g := NewGeoIP(testMMDB, nil)
	lat, lon, city, country, ok := g.Lookup("1.1.1.1")
	if !ok {
		t.Fatal("expected 1.1.1.1 to resolve")
	}
	if lat != 12.5 || lon != -34.25 {
		t.Errorf("coords = %v,%v, want 12.5,-34.25", lat, lon)
	}
	if city != "Testville" || country != "TC" {
		t.Errorf("city/country = %q/%q, want Testville/TC", city, country)
	}
	if _, _, _, _, ok := g.Lookup("9.9.9.9"); ok {
		t.Error("9.9.9.9 is not in the fixture, should be !ok")
	}
	if _, _, _, _, ok := g.Lookup("not-an-ip"); ok {
		t.Error("malformed IP should be !ok")
	}
}

func TestGeoIPLookupNoDatabase(t *testing.T) {
	g := NewGeoIP(filepath.Join(t.TempDir(), "absent.mmdb"), nil)
	if _, _, _, _, ok := g.Lookup("1.1.1.1"); ok {
		t.Error("lookup with no database loaded should be !ok")
	}
}

func TestGeoIPEnsureFreshDownloadsAndCaches(t *testing.T) {
	body := gzipFixture(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dbip-city-lite-2026-06.mmdb.gz" {
			atomic.AddInt32(&hits, 1)
			w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	g := NewGeoIP(filepath.Join(t.TempDir(), "geo.mmdb"), nil)
	g.baseURL = srv.URL

	if err := g.EnsureFresh(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, ok := g.Lookup("1.1.1.1"); !ok {
		t.Error("lookup should work after download")
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("hits = %d, want 1", n)
	}
	// Same month again: must not re-download.
	if err := g.EnsureFresh(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("re-downloaded within the same month: hits = %d, want 1", n)
	}
}

func TestGeoIPEnsureFreshFallsBackToPreviousMonth(t *testing.T) {
	body := gzipFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Current month not yet published; previous month is available.
		if r.URL.Path == "/dbip-city-lite-2026-05.mmdb.gz" {
			w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	g := NewGeoIP(filepath.Join(t.TempDir(), "geo.mmdb"), nil)
	g.baseURL = srv.URL

	if err := g.EnsureFresh(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if g.loadedMonth != "2026-05" {
		t.Errorf("loadedMonth = %q, want 2026-05 (fallback)", g.loadedMonth)
	}
	if _, _, _, _, ok := g.Lookup("2.2.2.2"); !ok {
		t.Error("lookup should work after fallback download")
	}
}

// TestGeoIPConcurrentLookupAndRefresh exercises the reader-lifecycle hazard:
// many lookups running while the database is repeatedly re-downloaded (each
// swap closing/unmapping the previous reader). Run with -race.
func TestGeoIPConcurrentLookupAndRefresh(t *testing.T) {
	body := gzipFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	g := NewGeoIP(filepath.Join(t.TempDir(), "geo.mmdb"), nil)
	g.baseURL = srv.URL
	if err := g.download(context.Background(), "2026-06"); err != nil {
		t.Fatal(err)
	}

	// Swapper: each download swaps in a new reader and closes the old one.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 40; i++ {
			if err := g.download(context.Background(), "2026-06"); err != nil {
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
					g.Lookup("1.1.1.1")
				}
			}
		}()
	}
	wg.Wait()

	if _, _, _, _, ok := g.Lookup("1.1.1.1"); !ok {
		t.Error("lookup should still work after concurrent refreshes")
	}
}
