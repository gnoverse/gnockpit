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
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

// ASN resolves IP addresses to their autonomous-system number and organization
// using a DB-IP ASN Lite mmdb that it auto-downloads and refreshes monthly. All
// methods are safe for concurrent use; lookups never block on a refresh. It
// mirrors GeoIP's download/refresh machinery for the sibling ASN dataset.
type ASN struct {
	path    string
	baseURL string
	httpc   *http.Client
	logf    func(string, ...any)

	mu          sync.RWMutex
	reader      *maxminddb.Reader
	loadedMonth string // "YYYY-MM" of the loaded data, or "" if none
}

// NewASN returns an ASN backed by the mmdb at path, opening it if it already
// exists so lookups work before the first refresh completes. logf (may be nil)
// is invoked on a successful refresh; it must be safe to call from the refresh
// goroutine.
func NewASN(path string, logf func(string, ...any)) *ASN {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	a := &ASN{
		path:    path,
		baseURL: dbipBaseURL,
		httpc:   &http.Client{Timeout: geoipDownloadTimeout},
		logf:    logf,
	}
	a.openExisting()
	return a
}

// openExisting loads an mmdb already on disk, taking the data month from the
// file's modification time so a recently-downloaded file isn't re-fetched.
func (a *ASN) openExisting() {
	r, err := maxminddb.Open(a.path)
	if err != nil {
		return
	}
	month := ""
	if fi, err := os.Stat(a.path); err == nil {
		month = fi.ModTime().UTC().Format("2006-01")
	}
	a.mu.Lock()
	a.reader = r
	a.loadedMonth = month
	a.mu.Unlock()
}

// Lookup returns the AS number and organization for an IP. ok is false when no
// database is loaded, the IP is malformed, or the database has no entry for it.
func (a *ASN) Lookup(ipStr string) (asn uint, org string, ok bool) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return 0, "", false
	}
	// Hold the read lock across the whole lookup: download() closes the old
	// reader after swapping it out, which unmaps its memory.
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.reader == nil {
		return 0, "", false
	}
	var rec struct {
		Number uint   `maxminddb:"autonomous_system_number"`
		Org    string `maxminddb:"autonomous_system_organization"`
	}
	if err := a.reader.Lookup(ip, &rec); err != nil {
		return 0, "", false
	}
	if rec.Org == "" {
		return 0, "", false
	}
	return rec.Number, rec.Org, true
}

// EnsureFresh downloads the current month's database if a different month (or
// nothing) is loaded, falling back to the previous month when the current one
// isn't published yet.
func (a *ASN) EnsureFresh(ctx context.Context, now time.Time) error {
	cur := now.UTC().Format("2006-01")
	a.mu.RLock()
	loaded := a.loadedMonth
	a.mu.RUnlock()
	if loaded == cur {
		return nil
	}
	var lastErr error
	for _, month := range []string{cur, prevMonth(now)} {
		if err := a.download(ctx, month); err != nil {
			lastErr = err
			continue
		}
		a.logf("asn: loaded DB-IP ASN Lite %s", month)
		return nil
	}
	return lastErr
}

// download fetches and decompresses a month's mmdb, validates it, atomically
// replaces the on-disk file, and swaps in the new reader.
func (a *ASN) download(ctx context.Context, month string) error {
	url := fmt.Sprintf("%s/dbip-asn-lite-%s.mmdb.gz", a.baseURL, month)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := a.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("asn download %s: status %d", url, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("asn gzip %s: %w", url, err)
	}
	defer gz.Close()

	tmp, err := os.CreateTemp(filepath.Dir(a.path), ".asn-*.tmp")
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
		return fmt.Errorf("asn open downloaded %s: %w", month, err)
	}
	if err := os.Rename(tmpName, a.path); err != nil {
		r.Close()
		os.Remove(tmpName)
		return err
	}

	a.mu.Lock()
	old := a.reader
	a.reader = r
	a.loadedMonth = month
	a.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

// providerPatterns maps a canonical cloud-provider name to lowercase substrings
// that appear in AS-organization names. First match wins.
var providerPatterns = []struct {
	name    string
	needles []string
}{
	{"AWS", []string{"amazon"}},
	{"GCP", []string{"google"}},
	{"Azure", []string{"microsoft", "azure"}},
	{"Hetzner", []string{"hetzner"}},
	{"OVH", []string{"ovh"}},
	{"DigitalOcean", []string{"digitalocean", "digital ocean"}},
	{"Contabo", []string{"contabo"}},
	{"Linode", []string{"linode", "akamai"}},
	{"Scaleway", []string{"scaleway"}},
	{"Vultr", []string{"vultr", "choopa"}},
	{"Oracle", []string{"oracle"}},
	{"Alibaba", []string{"alibaba", "aliyun"}},
	{"Tencent", []string{"tencent"}},
	{"Cloudflare", []string{"cloudflare"}},
	{"Leaseweb", []string{"leaseweb"}},
}

// Provider maps an AS-organization name to a canonical cloud-provider label,
// falling back to the raw organization name when no known provider matches.
func Provider(asOrg string) string {
	if asOrg == "" {
		return ""
	}
	low := strings.ToLower(asOrg)
	for _, p := range providerPatterns {
		for _, n := range p.needles {
			if strings.Contains(low, n) {
				return p.name
			}
		}
	}
	return asOrg
}
