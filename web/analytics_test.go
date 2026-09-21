package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAnalytics(t *testing.T) {
	for _, tc := range []struct {
		spec     string
		wantErr  bool
		provider string
	}{
		{spec: "", provider: ""},
		{spec: "   ", provider: ""},
		{spec: "simple-analytics", provider: "simple-analytics"},
		{spec: "simpleanalytics", provider: "simple-analytics"},
		{spec: "  Simple-Analytics  ", provider: "simple-analytics"},
		{spec: "plausible", wantErr: true},
		{spec: "simple analytics", wantErr: true},
		{spec: "https://scripts.simpleanalyticscdn.com/latest.js", wantErr: true},
	} {
		got, err := ParseAnalytics(tc.spec)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseAnalytics(%q) should have errored", tc.spec)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAnalytics(%q): %v", tc.spec, err)
			continue
		}
		if got.Provider != tc.provider {
			t.Errorf("ParseAnalytics(%q).Provider = %q, want %q", tc.spec, got.Provider, tc.provider)
		}
		if got.Enabled() != (tc.provider != "") {
			t.Errorf("ParseAnalytics(%q).Enabled() = %v", tc.spec, got.Enabled())
		}
	}
}

func TestAnalyticsInject(t *testing.T) {
	sa, err := ParseAnalytics("simple-analytics")
	if err != nil {
		t.Fatal(err)
	}
	page := []byte("<html><body><p>hi</p></body></html>")

	// Disabled returns the very same backing array, not just equal bytes:
	// the no-analytics path must not copy the 240 KB dashboard.
	got := Analytics{}.inject(page)
	if &got[0] != &page[0] {
		t.Error("disabled inject copied the page")
	}

	out := string(sa.inject(page))
	script := "https://scripts.simpleanalyticscdn.com/latest.js"
	if !strings.Contains(out, script) {
		t.Fatalf("snippet missing from %q", out)
	}
	if !strings.Contains(out, "https://queue.simpleanalyticscdn.com/noscript.gif") {
		t.Errorf("noscript pixel missing from %q", out)
	}
	// Inside the body, and after the existing content.
	if i, j := strings.Index(out, script), strings.Index(out, "</body>"); i > j {
		t.Errorf("snippet at %d lands after </body> at %d", i, j)
	}
	if i, j := strings.Index(out, "<p>hi</p>"), strings.Index(out, script); i > j {
		t.Errorf("snippet at %d lands before the page content at %d", j, i)
	}
	if !strings.HasSuffix(out, "</body></html>") {
		t.Errorf("tail rewritten: %q", out)
	}

	// A page with no </body> is left alone rather than gaining a stray snippet.
	headless := []byte("<html><p>no body tag</p></html>")
	if string(sa.inject(headless)) != string(headless) {
		t.Error("inject touched a page with no </body>")
	}
}

func TestAnalyticsInjectIndent(t *testing.T) {
	sa, err := ParseAnalytics("simple-analytics")
	if err != nil {
		t.Fatal(err)
	}

	// index.html closes with </body> alone on an indented line, and the block
	// should adopt that indent instead of hanging off the left margin.
	got := string(sa.inject([]byte("<html>\n    <body>\n        <p>hi</p>\n    </body>\n</html>\n")))
	want := "<html>\n    <body>\n        <p>hi</p>\n" +
		"    <!-- Simple Analytics: privacy-first, no cookies, no personal data. -->\n" +
		"    <script async src=\"https://scripts.simpleanalyticscdn.com/latest.js\"></script>\n" +
		"    <noscript><img src=\"https://queue.simpleanalyticscdn.com/noscript.gif\" alt=\"\" referrerpolicy=\"no-referrer-when-downgrade\" /></noscript>\n" +
		"    </body>\n</html>\n"
	if got != want {
		t.Errorf("indented inject:\n got %q\nwant %q", got, want)
	}

	// The real page must keep its </body> line intact.
	page, err := content.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sa.inject(page)), "</noscript>\n    </body>\n") {
		t.Error("index.html </body> line lost its indentation")
	}
}

func TestHandleIndexAnalytics(t *testing.T) {
	script := "scripts.simpleanalyticscdn.com"

	// Default: the dashboard ships with no third-party origin at all.
	w := httptest.NewRecorder()
	(&Server{}).handleIndex(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	plain := w.Body.String()
	if strings.Contains(plain, script) {
		t.Error("analytics served without --analytics")
	}

	sa, err := ParseAnalytics("simple-analytics")
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	(&Server{Analytics: sa}).handleIndex(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	withSA := w.Body.String()
	if !strings.Contains(withSA, script) {
		t.Error("analytics not served with --analytics simple-analytics")
	}
	// The handler must go through inject, not carry its own copy of the splice.
	if want := string(sa.inject([]byte(plain))); withSA != want {
		t.Error("handler output differs from inject()")
	}
}
