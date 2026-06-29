package node

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

// dbipBaseURL is the DB-IP free download host. The monthly IP-to-City Lite
// file is "<base>/dbip-city-lite-YYYY-MM.mmdb.gz" (CC-BY-4.0).
const dbipBaseURL = "https://download.db-ip.com/free"

// geoipDownloadTimeout bounds a whole database download (request + body read).
const geoipDownloadTimeout = 5 * time.Minute

// GeoIP resolves IP addresses to coordinates using a DB-IP City Lite mmdb that
// it auto-downloads and refreshes monthly. All methods are safe for concurrent
// use; lookups never block on a refresh.
type GeoIP struct {
	path    string
	baseURL string
	httpc   *http.Client
	logf    func(string, ...any)

	mu          sync.RWMutex
	reader      *maxminddb.Reader
	loadedMonth string // "YYYY-MM" of the loaded data, or "" if none
}

// NewGeoIP returns a GeoIP backed by the mmdb at path, opening it if it already
// exists so lookups work before the first refresh completes. logf (may be nil)
// is invoked on a successful refresh; it must be safe to call from the refresh
// goroutine.
func NewGeoIP(path string, logf func(string, ...any)) *GeoIP {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	g := &GeoIP{
		path:    path,
		baseURL: dbipBaseURL,
		httpc:   &http.Client{Timeout: geoipDownloadTimeout},
		logf:    logf,
	}
	g.openExisting()
	return g
}

// openExisting loads an mmdb already on disk. The data month is taken from the
// file's modification time so a recently-downloaded file isn't re-fetched.
func (g *GeoIP) openExisting() {
	r, err := maxminddb.Open(g.path)
	if err != nil {
		return
	}
	month := ""
	if fi, err := os.Stat(g.path); err == nil {
		month = fi.ModTime().UTC().Format("2006-01")
	}
	g.mu.Lock()
	g.reader = r
	g.loadedMonth = month
	g.mu.Unlock()
}

// Lookup returns the coordinates for an IP. ok is false when no database is
// loaded, the IP is malformed, or the database has no location for it.
func (g *GeoIP) Lookup(ipStr string) (lat, lon float64, city, country string, ok bool) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return 0, 0, "", "", false
	}
	// Hold the read lock across the whole lookup. download() closes the old
	// reader after swapping it out, which unmaps its memory; closing must wait
	// for in-flight lookups to drain, or Lookup would read from unmapped pages.
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.reader == nil {
		return 0, 0, "", "", false
	}
	var rec struct {
		City struct {
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"city"`
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
		Location struct {
			Latitude  float64 `maxminddb:"latitude"`
			Longitude float64 `maxminddb:"longitude"`
		} `maxminddb:"location"`
	}
	if err := g.reader.Lookup(ip, &rec); err != nil {
		return 0, 0, "", "", false
	}
	// DB-IP omits the location entirely for unlocated ranges, which decodes to
	// the zero coordinate (a point in the ocean) — treat that as "no location".
	if rec.Location.Latitude == 0 && rec.Location.Longitude == 0 {
		return 0, 0, "", "", false
	}
	return rec.Location.Latitude, rec.Location.Longitude, rec.City.Names["en"], rec.Country.ISOCode, true
}

// EnsureFresh downloads the current month's database if a different month (or
// nothing) is loaded. If the current month isn't published yet it falls back to
// the previous month.
func (g *GeoIP) EnsureFresh(ctx context.Context, now time.Time) error {
	cur := now.UTC().Format("2006-01")
	g.mu.RLock()
	loaded := g.loadedMonth
	g.mu.RUnlock()
	if loaded == cur {
		return nil
	}
	var lastErr error
	for _, month := range []string{cur, prevMonth(now)} {
		if err := g.download(ctx, month); err != nil {
			lastErr = err
			continue
		}
		g.logf("geoip: loaded DB-IP City Lite %s", month)
		return nil
	}
	return lastErr
}

// prevMonth returns the "YYYY-MM" of the month before now.
func prevMonth(now time.Time) string {
	firstOfMonth := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	return firstOfMonth.AddDate(0, 0, -1).Format("2006-01")
}

// download fetches and decompresses a month's mmdb, validates it, atomically
// replaces the on-disk file, and swaps in the new reader.
func (g *GeoIP) download(ctx context.Context, month string) error {
	url := fmt.Sprintf("%s/dbip-city-lite-%s.mmdb.gz", g.baseURL, month)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := g.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("geoip download %s: status %d", url, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("geoip gzip %s: %w", url, err)
	}
	defer gz.Close()

	tmp, err := os.CreateTemp(filepath.Dir(g.path), ".geoip-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, gz); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	tmp.Close()

	r, err := maxminddb.Open(tmpName)
	if err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("geoip open downloaded %s: %w", month, err)
	}
	if err := os.Rename(tmpName, g.path); err != nil {
		r.Close()
		os.Remove(tmpName)
		return err
	}

	g.mu.Lock()
	old := g.reader
	g.reader = r
	g.loadedMonth = month
	g.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}
