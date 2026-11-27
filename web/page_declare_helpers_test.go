//go:build page

package web

import (
	"bytes"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gnolang/gno/tm2/pkg/amino"
	"github.com/gnolang/gno/tm2/pkg/crypto"
	"github.com/gnolang/gno/tm2/pkg/std"
	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/location"
	"github.com/gnoverse/gnockpit/location/locationtest"
)

// The validator page's Location section, the page's last, and its parts:
// the signed document folded under its toggle, the section's last part.
const (
	declareSection      = "#val-body [data-section=location]"
	declareDocToggle    = declareSection + " > details.decl-doc:last-child > summary"
	declareSigned       = declareSection + " pre.decl-signed"
	declareCopy         = declareSection + " button.decl-copy"
	declareDownloadLink = declareSection + " a.decl-download"
)

// The Location section's actions, its form, and the steps that sign the
// document it builds: the commands that sign it and the field the signed one
// is pasted in.
const (
	declareOpen      = declareSection + " button.decl-open"
	declareRemove    = declareSection + " button.decl-remove"
	declareForm      = declareSection + " form.decl-form"
	declareMap       = declareForm + " svg.decl-map"
	declarePin       = declareMap + " circle.decl-pin"
	declareFields    = declareForm + " .decl-fields"
	declareLat       = declareForm + " input[name=lat]"
	declareLon       = declareForm + " input[name=lon]"
	declareCountry   = declareForm + " select[name=country]"
	declareProvider  = declareForm + " select[name=provider]"
	declareProviders = declareProvider + " > option"
	declareOther     = declareForm + " input[name=provider-name]"
	declareBuild     = declareForm + " button.decl-build"
	declareSigning   = declareSection + " div.decl-sign"
	declareSnippet   = declareSection + " pre.decl-snippet"
	declarePaste     = declareSection + " textarea[name=signed]"
	declareSubmit    = declareSection + " button.decl-submit"
	declareResult    = declareSection + " div.decl-result.err[role=status]"
)

// declareStatus is the Location section's own status: what Continue or
// Remove built, or why no document builds.
const declareStatus = declareSection + " > div[role=status]"

// declareNote is the validator page's note of a declaration stored, outside
// its body, which the page draws again.
const declareNote = "#val-page #val-note[role=status]"

// declareCopySnippet is the ⧉ copy button of the commands that sign the
// document built.
const declareCopySnippet = declareSection + " button.decl-snippet-copy"

// declareSnippetHead and declareSnippetTail enclose the document in the
// commands that sign it: a quoted here-document.
const (
	declareSnippetHead = "command \\cat >| gnockpit-location.json <<'EOF'\n"
	declareSnippetTail = "\nEOF\n"
)

// declareMapWidth and declareMapHeight are the form's map's viewBox: x =
// lon + 180, y = 90 − lat.
const (
	declareMapWidth  = 360
	declareMapHeight = 180
)

// declareGridValue matches a coordinate field's value on the 0.1° grid: at
// most one decimal.
var declareGridValue = regexp.MustCompile(`^-?[0-9]+(\.[0-9])?$`)

// declareLocationInfo is the popover of a declared location's ⓘs: the status
// line's and the Location section's title's.
const declareLocationInfo = "What: where its signing node is, as its operator declared it\n" +
	"Source: a document its operator signed with its operator key (gnokey), which gnockpit verified\n" +
	"Scored: no, shown for monitoring"

// declareDataURL matches the download link's address: a data: URL of a JSON
// document.
var declareDataURL = regexp.MustCompile(`^data:application/json[;,]`)

// declareFileName is the name the signed document of key's operator downloads
// under.
func declareFileName(key crypto.PrivKey) string {
	return "gnockpit-location-" + keyAddress(key) + ".json"
}

// declareExpectSigned waits until the Location section shows want as its
// signed document, then checks it holds want exactly: ToHaveText reads text
// with its white space collapsed.
func declareExpectSigned(e *pageEnv, want string) {
	e.T.Helper()
	signed := e.Page.Locator(declareSigned)
	e.must(e.Expect.Locator(signed).ToHaveText(want))
	got, err := signed.TextContent()
	e.must(err)
	if got != want {
		e.T.Fatalf("signed document = %q, want %q", got, want)
	}
}

// declareDownload presses the Location section's ⤓ download and returns the
// name the browser saves the file under and what the file holds.
func declareDownload(e *pageEnv) (name, body string) {
	e.T.Helper()
	dl, err := e.Page.ExpectDownload(func() error {
		e.press(e.Page.Locator(declareDownloadLink))
		return nil
	})
	if err != nil {
		e.T.Fatalf("download the signed document: %v", err)
	}
	path, err := dl.Path()
	if err != nil {
		e.T.Fatalf("the downloaded document's path: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		e.T.Fatalf("read the downloaded document: %v", err)
	}
	return dl.SuggestedFilename(), string(b)
}

// declareAwaitLive waits until the dashboard has the server's first message:
// the peers and the declared locations the form lists providers from, and the
// server's clock the record's time is read on.
func declareAwaitLive(e *pageEnv) {
	e.T.Helper()
	e.must(e.expect("#d-network").ToHaveText(pageChainID))
}

// declareOpenForm opens the Location section's form, once the dashboard has
// the server's first message.
func declareOpenForm(e *pageEnv) {
	e.T.Helper()
	declareAwaitLive(e)
	e.press(e.Page.Locator(declareOpen))
	e.must(e.expect(declareForm).ToBeVisible())
}

// declareNotice is the notice that only the operator can declare: the
// form's first part, and the first of a removal's signing steps.
const declareNotice = "p.decl-notice"

// declareNoticeText is the notice's text for the operator at address.
func declareNoticeText(address string) string {
	return "Only this validator's operator can declare its location: gnockpit accepts the document only when the operator key of " +
		address + " signs it."
}

// declareExpectNotice checks that the first part of the element selector is
// the notice for the operator at address, exactly, its "operator key" bold,
// and no live region.
func declareExpectNotice(e *pageEnv, selector, address string) {
	e.T.Helper()
	first := selector + " > :first-child"
	e.must(e.expect(first).ToHaveClass("decl-notice"))
	if got, want := declareTextOf(e, first), declareNoticeText(address); got != want {
		e.T.Errorf("the notice of %s = %q, want %q", selector, got, want)
	}
	e.must(e.expect(first + " > strong").ToHaveText("operator key"))
	e.must(e.expect(first + " > strong").ToHaveCount(1))
	e.must(e.expect(first + ":is([role], [aria-live])").ToHaveCount(0))
}

// declareBordersPath is where the page reads the country borders.
const declareBordersPath = "/data/country-borders.json"

// declareAwaitBorders waits until the page's read of the country borders,
// which the form's opening starts, has settled: read, or failed. It awaits
// the page's own read (country-borders.js's readBorders), which starts a
// read of its own when none is under way and none succeeded.
func declareAwaitBorders(e *pageEnv) {
	e.T.Helper()
	_, err := e.Page.Evaluate(`async () => (await import('/js/country-borders.js')).readBorders()`)
	e.must(err)
}

// declareSetField fills the form's field selector with value and leaves it,
// as a user moving on to the next field does.
func declareSetField(e *pageEnv, selector, value string) {
	e.T.Helper()
	field := e.Page.Locator(selector)
	e.must(field.Fill(value))
	e.must(field.Blur())
}

// declareFill fills the form: lat and lon, the country by its code, and the
// provider by its option's value ("" for none).
func declareFill(e *pageEnv, lat, lon, country, provider string) {
	e.T.Helper()
	declareSetField(e, declareLat, lat)
	declareSetField(e, declareLon, lon)
	declareSelectCountry(e, country)
	declareSelectProvider(e, provider)
}

// declareSelectProvider selects the provider option of value: "" for none,
// on-premise or a listed provider.
func declareSelectProvider(e *pageEnv, value string) {
	e.T.Helper()
	if _, err := e.Page.Locator(declareProvider).SelectOption(playwright.SelectOptionValues{Values: &[]string{value}}); err != nil {
		e.T.Fatalf("select the provider %q: %v", value, err)
	}
}

// declareChooseOther chooses Other… as the provider.
func declareChooseOther(e *pageEnv) {
	e.T.Helper()
	if _, err := e.Page.Locator(declareProvider).SelectOption(playwright.SelectOptionValues{Labels: &[]string{"Other…"}}); err != nil {
		e.T.Fatalf("choose Other… as the provider: %v", err)
	}
}

// declareOtherProvider chooses Other… as the provider and fills the field it
// reveals with name.
func declareOtherProvider(e *pageEnv, name string) {
	e.T.Helper()
	declareChooseOther(e)
	declareSetField(e, declareOther, name)
}

// declareSelectCountry selects the country of code in the form.
func declareSelectCountry(e *pageEnv, code string) {
	e.T.Helper()
	if _, err := e.Page.Locator(declareCountry).SelectOption(playwright.SelectOptionValues{Values: &[]string{code}}); err != nil {
		e.T.Fatalf("select the country %s: %v", code, err)
	}
}

// declareClickMap clicks the form's map at (x, y) of its viewBox, or taps it
// there on a touch screen. The map has no border or padding: its box is its
// viewBox's.
func declareClickMap(e *pageEnv, x, y float64) {
	e.T.Helper()
	m := e.Page.Locator(declareMap)
	box, err := m.BoundingBox()
	if err != nil {
		e.T.Fatalf("the form's map's box: %v", err)
	}
	at := &playwright.Position{X: x / declareMapWidth * box.Width, Y: y / declareMapHeight * box.Height}
	if e.profile.touch() {
		e.must(m.Tap(playwright.LocatorTapOptions{Position: at}))
		return
	}
	e.must(m.Click(playwright.LocatorClickOptions{Position: at}))
}

// declareCoordinate reads the form's coordinate field selector, which must
// hold a value on the 0.1° grid.
func declareCoordinate(e *pageEnv, selector string) float64 {
	e.T.Helper()
	v, err := e.Page.Locator(selector).InputValue()
	e.must(err)
	if !declareGridValue.MatchString(v) {
		e.T.Fatalf("%s = %q, want a value with at most one decimal", selector, v)
	}
	x, err := strconv.ParseFloat(v, 64)
	e.must(err)
	return x
}

// declarePinAt checks that the map's pin stands at (lat, lon): x = lon + 180,
// y = 90 − lat.
func declarePinAt(e *pageEnv, lat, lon float64) {
	e.T.Helper()
	pin := e.Page.Locator(declarePin)
	e.must(e.Expect.Locator(pin).ToBeVisible())
	for _, c := range []struct {
		attr string
		want float64
	}{{"cx", lon + 180}, {"cy", 90 - lat}} {
		v, err := pin.GetAttribute(c.attr)
		e.must(err)
		got, err := strconv.ParseFloat(v, 64)
		if err != nil || math.Abs(got-c.want) > 0.01 {
			e.T.Errorf("the pin's %s = %q, want %.2f", c.attr, v, c.want)
		}
	}
}

// declareTextOf waits until the element selector shows, then returns its text
// exactly: ToHaveText reads text with its white space collapsed.
func declareTextOf(e *pageEnv, selector string) string {
	e.T.Helper()
	l := e.Page.Locator(selector)
	e.must(e.Expect.Locator(l).ToBeVisible())
	got, err := l.TextContent()
	e.must(err)
	return got
}

// declareBuildDocument presses Continue and returns the document built.
func declareBuildDocument(e *pageEnv) string {
	e.T.Helper()
	e.press(e.Page.Locator(declareBuild))
	return declareDocument(e)
}

// declareDocument returns the document the Location section's commands sign:
// their here-document.
func declareDocument(e *pageEnv) string {
	e.T.Helper()
	snippet := declareTextOf(e, declareSnippet)
	rest, headed := strings.CutPrefix(snippet, declareSnippetHead)
	doc, _, ended := strings.Cut(rest, declareSnippetTail)
	if !headed || !ended {
		e.T.Fatalf("the commands =\n%s\nwant a here-document between %q and %q", snippet, declareSnippetHead, declareSnippetTail)
	}
	return doc
}

// declareTx decodes doc as gnokey reads a transaction: amino JSON.
func declareTx(e *pageEnv, doc []byte) std.Tx {
	e.T.Helper()
	var tx std.Tx
	if err := amino.UnmarshalJSON(doc, &tx); err != nil {
		e.T.Fatalf("decode the document %s: %v", doc, err)
	}
	return tx
}

// declareExpectDocument checks that doc is the unsigned document of r: as
// amino decodes them, doc and locationtest's document of r are one
// transaction, a send of r's operator to itself whose memo carries r.
func declareExpectDocument(e *pageEnv, doc string, r location.Record) {
	e.T.Helper()
	memo := locationtest.Memo(r)
	got, want := declareTx(e, []byte(doc)), declareTx(e, locationtest.Document(r.Operator, memo))
	if got.Memo != memo {
		e.T.Errorf("the document's memo = %q, want %q", got.Memo, memo)
	}
	gotJSON, err := amino.MarshalJSON(got)
	e.must(err)
	wantJSON, err := amino.MarshalJSON(want)
	e.must(err)
	if string(gotJSON) != string(wantJSON) {
		e.T.Errorf("the document reads, as amino decodes it,\n%s\nwant\n%s", gotJSON, wantJSON)
	}
}

// declarePasteSigned signs the document the section shows with key, as
// gnokey v1.1.0 signs it, and pastes it with a newline before and after, as
// a terminal selection holds it.
func declarePasteSigned(e *pageEnv, key crypto.PrivKey) {
	e.T.Helper()
	doc := declareDocument(e)
	signed := locationtest.Sign(e.T, []byte(doc), key, location.SignChainID, location.Legacy)
	e.must(e.Page.Locator(declarePaste).Fill("\n" + string(signed) + "\n"))
}

// declareSubmitSigned pastes the document the section shows signed with key
// (declarePasteSigned) and presses Submit.
func declareSubmitSigned(e *pageEnv, key crypto.PrivKey) {
	e.T.Helper()
	declarePasteSigned(e, key)
	e.press(e.Page.Locator(declareSubmit))
}

// declareOption is an option of the form's provider select.
type declareOption struct{ label, value string }

// declareExpectProviders checks the options the form's provider select
// offers, in order.
func declareExpectProviders(e *pageEnv, want ...declareOption) {
	e.T.Helper()
	options := e.Page.Locator(declareProviders)
	e.must(e.Expect.Locator(options).ToHaveCount(len(want)))
	v, err := options.EvaluateAll(`(os) => os.flatMap((o) => [o.textContent, o.value])`)
	e.must(err)
	texts, ok := v.([]any)
	if !ok || len(texts) != 2*len(want) {
		e.T.Fatalf("the provider options' labels and values: got %v, want %d pairs", v, len(want))
	}
	got := make([]declareOption, len(want))
	for i := range got {
		label, okLabel := texts[2*i].(string)
		value, okValue := texts[2*i+1].(string)
		if !okLabel || !okValue {
			e.T.Fatalf("the provider options' labels and values: got %v, want strings", v)
		}
		got[i] = declareOption{label, value}
	}
	if !slices.Equal(got, want) {
		e.T.Errorf("the provider select offers %q, want %q", got, want)
	}
}

// declareBehindCutAnswer starts a reverse proxy in front of the server that
// cuts the body of path's 200 answers in half, which is then not JSON, as a faulty
// intermediary may, and returns its URL.
func declareBehindCutAnswer(e *pageEnv, path string) string {
	e.T.Helper()
	target, err := url.Parse(e.ts.URL)
	if err != nil {
		e.T.Fatalf("parse the server URL: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(res *http.Response) error {
		if res.Request.URL.Path != path || res.StatusCode != http.StatusOK {
			return nil
		}
		b, err := io.ReadAll(res.Body)
		if err != nil {
			return err
		}
		if err := res.Body.Close(); err != nil {
			return err
		}
		cut := b[:len(b)/2]
		res.Body = io.NopCloser(bytes.NewReader(cut))
		res.ContentLength = int64(len(cut))
		res.Header.Set("Content-Length", strconv.Itoa(len(cut)))
		return nil
	}
	front := httptest.NewServer(proxy)
	e.T.Cleanup(front.Close)
	return front.URL
}

// declareExpectInDialog checks that the element selector lies inside the
// validator page's dialog as it shows, scrolled: within the dialog's box.
func declareExpectInDialog(e *pageEnv, selector string) {
	e.T.Helper()
	v, err := e.Page.Evaluate(`(sel) => {
		const el = document.querySelector(sel)
		if (!el) return null
		const r = el.getBoundingClientRect()
		const d = document.querySelector('#val-page').getBoundingClientRect()
		return [r.top, r.bottom, d.top, d.bottom]
	}`, selector)
	e.must(err)
	list, ok := v.([]any)
	if !ok || len(list) != 4 {
		e.T.Fatalf("the boxes of %s and the dialog: got %v", selector, v)
	}
	var at [4]float64
	for i, x := range list {
		switch n := x.(type) {
		case int:
			at[i] = float64(n)
		case float64:
			at[i] = n
		default:
			e.T.Fatalf("the boxes of %s and the dialog: got %v", selector, v)
		}
	}
	if top, bottom, dTop, dBottom := at[0], at[1], at[2], at[3]; top < dTop || bottom > dBottom {
		e.T.Errorf("%s spans %v..%v, outside the dialog's %v..%v", selector, top, bottom, dTop, dBottom)
	}
}
