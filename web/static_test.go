package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// strongETag matches a strong entity tag: an opaque tag in double quotes,
// without the W/ of a weak one (RFC 9110, section 8.8.3).
var strongETag = regexp.MustCompile(`^"[\x21\x23-\x7e]+"$`)

// scriptsFS is a tree laid out as the binary embeds web/: the page beside
// its js directory, whose a.js holds script.
func scriptsFS(script string) fstest.MapFS {
	return fstest.MapFS{
		"index.html":        {Data: []byte("<html></html>")},
		"service-worker.js": {Data: []byte("self.addEventListener('push', () => {})\n")},
		"js/a.js":           {Data: []byte(script)},
		"js/a.txt":          {Data: []byte("not a script")},
		"js/sub/b.js":       {Data: []byte("export {}\n")},
		"js/lib.js/c.js":    {Data: []byte("export {}\n")},
	}
}

// requestStatic sends h a request for target, with an If-None-Match of etag
// unless it is empty.
func requestStatic(h http.Handler, method, target, etag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	if etag != "" {
		r.Header.Set("If-None-Match", etag)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// checkScriptHeaders checks the headers every answer with a script carries.
func checkScriptHeaders(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	for name, want := range map[string]string{
		"Content-Type":  "text/javascript; charset=utf-8",
		"Cache-Control": "no-cache",
	} {
		if got := w.Header().Get(name); got != want {
			t.Errorf("%s: %s = %q, want %q", what, name, got, want)
		}
	}
	if etag := w.Header().Get("ETag"); !strongETag.MatchString(etag) {
		t.Errorf("%s: ETag = %q, want a quoted strong entity tag", what, etag)
	}
}

func TestJSServed(t *testing.T) {
	const script = "export function start() {}\n"
	w := requestStatic(newJSHandler(scriptsFS(script)), http.MethodGet, "/js/a.js", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /js/a.js: status %d, want 200", w.Code)
	}
	checkScriptHeaders(t, "GET /js/a.js", w)
	if got := w.Body.String(); got != script {
		t.Errorf("GET /js/a.js: body %q, want %q", got, script)
	}
}

// TestJSRevalidates checks what a browser holding a cached script gets on
// its next load: nothing to download when the file is unchanged, and the
// file itself when a deploy changed it.
func TestJSRevalidates(t *testing.T) {
	previous := newJSHandler(scriptsFS("export const v = 1\n"))
	const script = "export const v = 2\n"
	current := newJSHandler(scriptsFS(script))
	cachedTag := requestStatic(previous, http.MethodGet, "/js/a.js", "").Header().Get("ETag")
	currentTag := requestStatic(current, http.MethodGet, "/js/a.js", "").Header().Get("ETag")
	if cachedTag == currentTag {
		t.Fatalf("two contents, one ETag %s", currentTag)
	}

	// The tag a browser cached from the previous deploy of the same content,
	// served by another handler instance (another process).
	sameContent := newJSHandler(scriptsFS(script))
	sameTag := requestStatic(sameContent, http.MethodGet, "/js/a.js", "").Header().Get("ETag")
	w := requestStatic(current, http.MethodGet, "/js/a.js", sameTag)
	if w.Code != http.StatusNotModified {
		t.Errorf("GET /js/a.js with the ETag of the same content from another handler: status %d, want 304", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("GET /js/a.js with the ETag of the same content from another handler: body %q, want none", w.Body.String())
	}

	w = requestStatic(current, http.MethodGet, "/js/a.js", cachedTag)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /js/a.js with another content's ETag: status %d, want 200", w.Code)
	}
	if got := w.Body.String(); got != script {
		t.Errorf("GET /js/a.js with another content's ETag: body %q, want %q", got, script)
	}
}

func TestJSNotFound(t *testing.T) {
	h := newJSHandler(scriptsFS("export {}\n"))
	for _, tc := range []struct{ name, target string }{
		{"the directory itself", "/js/"},
		{"a missing script", "/js/missing.js"},
		{"the page, outside js", "/js/../index.html"},
		{"a script outside js", "/js/../service-worker.js"},
		{"a directory", "/js/sub"},
		{"a directory named like a script", "/js/lib.js"},
		{"no script", "/js/a.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := requestStatic(h, http.MethodGet, tc.target, ""); w.Code != http.StatusNotFound {
				t.Errorf("GET %s: status %d, want 404; body %q", tc.target, w.Code, w.Body.String())
			}
		})
	}
}

func TestJSHead(t *testing.T) {
	w := requestStatic(newJSHandler(scriptsFS("export {}\n")), http.MethodHead, "/js/a.js", "")
	if w.Code != http.StatusOK {
		t.Fatalf("HEAD /js/a.js: status %d, want 200", w.Code)
	}
	checkScriptHeaders(t, "HEAD /js/a.js", w)
	if w.Body.Len() != 0 {
		t.Errorf("HEAD /js/a.js: body %q, want none", w.Body.String())
	}
}

func TestJSMethodNotAllowed(t *testing.T) {
	h := newJSHandler(scriptsFS("export {}\n"))
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			w := requestStatic(h, method, "/js/a.js", "")
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s /js/a.js: status %d, want 405", method, w.Code)
			}
			if got := w.Header().Get("Allow"); got != "GET, HEAD" {
				t.Errorf("%s /js/a.js: Allow = %q, want %q", method, got, "GET, HEAD")
			}
		})
	}
}

// TestIndexWithAnalyticsLoadsModules checks that the page served with
// --analytics still loads its entry module, with the analytics snippet
// injected after it, above </body>.
func TestIndexWithAnalyticsLoadsModules(t *testing.T) {
	sa, err := ParseAnalytics("simple-analytics", "")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	(&Server{Analytics: sa}).handleIndex(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /: status %d, want 200", w.Code)
	}
	page := w.Body.String()
	entry := strings.Index(page, `<script type="module" src="/js/main.js"></script>`)
	snippet := strings.Index(page, `<script async src="https://scripts.simpleanalyticscdn.com/latest.js"></script>`)
	closing := strings.LastIndex(page, "</body>")
	switch {
	case entry < 0:
		t.Error("GET /: no module script loading /js/main.js")
	case snippet < 0:
		t.Error("GET /: no analytics snippet")
	case !(entry < snippet && snippet < closing):
		t.Errorf("GET /: entry module at %d, snippet at %d, </body> at %d: want them in that order", entry, snippet, closing)
	}
}

// TestRoutesServeEmbeddedJS checks that the server serves the page's entry
// module from the binary's embedded files.
func TestRoutesServeEmbeddedJS(t *testing.T) {
	want, err := content.ReadFile("js/main.js")
	if err != nil {
		t.Fatal(err)
	}
	w := requestStatic((&Server{}).routes(), http.MethodGet, "/js/main.js", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /js/main.js: status %d, want 200", w.Code)
	}
	checkScriptHeaders(t, "GET /js/main.js", w)
	if got := w.Body.String(); got != string(want) {
		t.Errorf("GET /js/main.js: body %q, want the embedded js/main.js %q", got, want)
	}
}

// TestRoutesServeTheCountryBorders checks that the server serves the country
// borders the location form reads from the binary's embedded files: JSON,
// revalidated on every load against its content ETag.
func TestRoutesServeTheCountryBorders(t *testing.T) {
	want, err := content.ReadFile("data/country-borders.json")
	if err != nil {
		t.Fatal(err)
	}
	w := requestStatic((&Server{}).routes(), http.MethodGet, "/data/country-borders.json", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /data/country-borders.json: status %d, want 200", w.Code)
	}
	for name, want := range map[string]string{
		"Content-Type":  "application/json",
		"Cache-Control": "no-cache",
	} {
		if got := w.Header().Get(name); got != want {
			t.Errorf("GET /data/country-borders.json: %s = %q, want %q", name, got, want)
		}
	}
	if etag := w.Header().Get("ETag"); !strongETag.MatchString(etag) {
		t.Errorf("GET /data/country-borders.json: ETag = %q, want a quoted strong entity tag", etag)
	}
	if !bytes.Equal(w.Body.Bytes(), want) {
		t.Errorf("GET /data/country-borders.json: body of %d bytes, want the embedded data/country-borders.json's %d", w.Body.Len(), len(want))
	}
}

// TestDataNotFound checks that the data handler serves the JSON files of the
// data directory alone.
func TestDataNotFound(t *testing.T) {
	h := newDataHandler(fstest.MapFS{
		"index.html":      {Data: []byte("<html></html>")},
		"js/a.js":         {Data: []byte("export {}\n")},
		"data/a.json":     {Data: []byte("{}\n")},
		"data/a.txt":      {Data: []byte("not JSON")},
		"data/sub/b.json": {Data: []byte("{}\n")},
	})
	if w := requestStatic(h, http.MethodGet, "/data/a.json", ""); w.Code != http.StatusOK {
		t.Errorf("GET /data/a.json: status %d, want 200", w.Code)
	}
	for _, target := range []string{"/data/", "/data/missing.json", "/data/../index.html", "/data/../js/a.js", "/data/sub", "/data/a.txt"} {
		if w := requestStatic(h, http.MethodGet, target, ""); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404; body %q", target, w.Code, w.Body.String())
		}
	}
}
