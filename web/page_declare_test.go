//go:build page

package web

import (
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/location"
	"github.com/gnoverse/gnockpit/node"
)

// TestPageDeclaredLocation checks a declared location on its validator's
// page: charlie, declared in Singapore at OVH, its peer unlocated. The status
// line writes the country, the provider, the coordinates and the day its
// operator declared them, its ⓘ saying where they come from, as the Location
// section's title's ⓘ does; the section folds the signed document under its
// toggle, after the buttons that declare; opened, it shows the document,
// which ⧉ copy, named for it, copies and ⤓ download saves under its
// operator's name.
func TestPageDeclaredLocation(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := declaredNetwork(t)
		declared := charlieInSingapore(t)
		sc.declarations = []location.Declaration{declared}
		e := openPage(t, p, sc, withURL("/?val="+valCharlie.address()))
		e.expectValidatorReport("charlie")

		where := e.validatorStatusSpans().Last()
		day := declared.At.UTC().Format(time.DateOnly)
		e.must(e.Expect.Locator(where).ToHaveText("🇸🇬 Singapore · OVH · 1.3, 103.8 · declared by the operator on " + day + "ⓘ"))
		e.must(e.Expect.Locator(where.Locator(".info-btn")).ToHaveAttribute("data-popover", declareLocationInfo))

		e.must(e.expect(declareSection + " > h3").ToHaveText("Locationⓘ"))
		e.must(e.expect(sectionInfo("location")).ToHaveAttribute("data-popover", declareLocationInfo))
		e.must(e.expect(declareDocToggle).ToHaveText("Signed document"))
		// A tap target of 24 px at least (WCAG 2.5.8).
		toggle, err := e.Page.Locator(declareDocToggle).BoundingBox()
		e.must(err)
		if toggle.Height < 24 {
			t.Errorf("the Signed document toggle is %v px tall, want 24 or more", toggle.Height)
		}
		e.must(e.expect(declareSigned).ToBeHidden())
		e.must(e.expect(declareCopy).ToBeHidden())
		e.press(e.Page.Locator(declareDocToggle))
		e.must(e.expect(declareSigned).ToBeVisible())
		declareExpectSigned(e, declared.Signed)

		e.must(e.expect(declareCopy).ToHaveText("⧉ copy"))
		e.must(e.expect(declareCopy).ToHaveAccessibleName("copy the signed document"))
		e.press(e.Page.Locator(declareCopy))
		e.must(e.expect(".copy-toast").ToHaveText("Copied!"))
		if p.browser == chromium {
			if got := e.clipboard(); got != declared.Signed {
				t.Errorf("clipboard = %q, want the signed document %q", got, declared.Signed)
			}
		}

		e.must(e.expect(declareDownloadLink).ToHaveText("⤓ download"))
		e.must(e.expect(declareDownloadLink).ToHaveAttribute("href", declareDataURL))
		name, body := declareDownload(e)
		if want := declareFileName(declOpCharlie); name != want {
			t.Errorf("downloaded file name = %q, want %q", name, want)
		}
		if body != declared.Signed {
			t.Errorf("downloaded document = %q, want the signed document %q", body, declared.Signed)
		}
	})
}

// TestPageDeclaredLocationHiddenWhileIPLocated checks that a validator
// gnockpit locates by its IP shows that location, its operator's declaration
// left out: bravo, in Germany at Hetzner by its IP, declared in Singapore.
// Once its peer loses its location, its page shows the declaration.
func TestPageDeclaredLocationHiddenWhileIPLocated(t *testing.T) {
	sc := declaredNetwork(t)
	declared := bravoInSingapore(t, "OVH")
	sc.declarations = []location.Declaration{declared}
	page := "/?val=" + valBravo.address()
	e := openPage(t, desktop, sc, withURL(page))
	e.expectValidatorReport("bravo")
	e.must(e.Expect.Locator(e.validatorStatusSpans().Last()).ToHaveText("🇩🇪 Germany · Hetznerⓘ"))
	e.must(e.expect(declareSection).ToHaveCount(0))

	// A snapshot of its own: the server took sc.snap and changes a snapshot
	// as it publishes it, so publish refuses one it took.
	unlocated := declaredNetwork(t).snap
	unlocate(unlocated, valBravo)
	e.publish(unlocated)
	e.navigate(page)
	e.expectValidatorReport("bravo")
	day := declared.At.UTC().Format(time.DateOnly)
	e.must(e.Expect.Locator(e.validatorStatusSpans().Last()).ToHaveText("🇸🇬 Singapore · OVH · 1.3, 103.8 · declared by the operator on " + day + "ⓘ"))
	declareExpectSigned(e, declared.Signed)
}

// TestPageDeclaredDocumentIsText checks that a declared provider holding
// markup shows as text in the status line, and the signed document as it is
// stored, no element in either.
func TestPageDeclaredDocumentIsText(t *testing.T) {
	const markup = "<b>x</b>"
	sc := declaredNetwork(t)
	r := charlieInSingapore(t).Record
	r.Provider = markup
	declared := declaration(t, declOpCharlie, r)
	sc.declarations = []location.Declaration{declared}
	e := openPage(t, desktop, sc, withURL("/?val="+valCharlie.address()))
	e.expectValidatorReport("charlie")

	day := declared.At.UTC().Format(time.DateOnly)
	e.must(e.Expect.Locator(e.validatorStatusSpans().Last()).ToHaveText("🇸🇬 Singapore · " + markup + " · 1.3, 103.8 · declared by the operator on " + day + "ⓘ"))
	e.must(e.expect("#val-body .val-status b").ToHaveCount(0))
	declareExpectSigned(e, declared.Signed)
	e.must(e.expect(declareSigned + " *").ToHaveCount(0))
}

// ---- Declaring

// TestPageDeclareOffered checks who the validator page offers a declaration
// to. Charlie, its peer unlocated, is offered Declare its location; once
// declared, Update, which a click or a tap opens filled with the declared
// location, its listed provider chosen, the focus left where it was, and
// Remove. Bravo, which gnockpit locates by its IP, and delta, whose
// operator has no valopers profile, have no Location section.
func TestPageDeclareOffered(t *testing.T) {
	t.Run("undeclared", func(t *testing.T) {
		onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
			e := openPage(t, p, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
			e.expectValidatorReport("charlie")
			e.must(e.expect(declareOpen).ToHaveText("Declare its location"))
			e.must(e.expect(declareRemove).ToHaveCount(0))
			e.must(e.expect(declareForm).ToHaveCount(0))

			e.navigate("/?val=" + valBravo.address())
			e.expectValidatorReport("bravo")
			e.must(e.expect(declareSection).ToHaveCount(0))
			e.navigate("/?val=" + valDelta.address())
			e.expectValidatorReport(shortAddr(opDelta))
			e.must(e.expect(declareSection).ToHaveCount(0))
		})
	})
	t.Run("declared", func(t *testing.T) {
		onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
			sc := declaredNetwork(t)
			sc.declarations = []location.Declaration{charlieInSingapore(t)}
			e := openPage(t, p, sc, withURL("/?val="+valCharlie.address()))
			e.expectValidatorReport("charlie")
			e.must(e.expect(declareOpen).ToHaveText("Update"))
			e.must(e.expect(declareRemove).ToHaveText("Remove"))

			declareOpenForm(e)
			// A click or a tap leaves the focus where it was: no field
			// raises a phone's keyboard over the map.
			e.must(e.expect(declareForm + " :is(input, select):focus").ToHaveCount(0))
			if p == desktop {
				e.must(e.expect(declareOpen).ToBeFocused())
			}
			e.must(e.expect(declareLat).ToHaveValue("1.3"))
			e.must(e.expect(declareLon).ToHaveValue("103.8"))
			e.must(e.expect(declareCountry).ToHaveValue("SG"))
			e.must(e.expect(declareCountry + " option:checked").ToHaveText("Singapore"))
			e.must(e.expect(declareProvider).ToHaveValue("OVH"))
			e.must(e.expect(declareOther).ToBeHidden())
			declarePinAt(e, 1.3, 103.8)
		})
	})
}

// TestPageDeclareOperatorNotice checks the notice that only the operator can
// declare, naming its operator's address, its "operator key" bold: on top of
// the form Declare its location and Update open, and on top of the steps that
// sign a removal, which takes the same signature.
func TestPageDeclareOperatorNotice(t *testing.T) {
	op := keyAddress(declOpCharlie)
	t.Run("declaring", func(t *testing.T) {
		onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
			e := openPage(t, p, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
			e.expectValidatorReport("charlie")
			declareOpenForm(e)
			declareExpectNotice(e, declareForm, op)
			e.must(e.expect(declareSection + " " + declareNotice).ToHaveCount(1))
		})
	})
	t.Run("declared", func(t *testing.T) {
		onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
			sc := declaredNetwork(t)
			sc.declarations = []location.Declaration{charlieInSingapore(t)}
			e := openPage(t, p, sc, withURL("/?val="+valCharlie.address()))
			e.expectValidatorReport("charlie")
			declareOpenForm(e)
			declareExpectNotice(e, declareForm, op)
			e.press(e.Page.Locator(declareRemove))
			e.must(e.expect(declareForm).ToHaveCount(0))
			declareExpectNotice(e, declareSigning, op)
			e.must(e.expect(declareSection + " " + declareNotice).ToHaveCount(1))
		})
	})
}

// TestPageDeclareBuildsTheDocument checks the form and the document its
// Continue builds: a click on the map sets the coordinates to its point on
// the 0.1° grid, and the pin there; a typed coordinate is rounded to the
// grid; a provider named under Other… as a listed one in another case
// becomes the listed name. The document is charlie's operator's send to itself whose
// memo carries the record, timed on the server's clock, and the commands
// sign it with the operator's key, printing it only once signed. The
// section's status says the document is built, as no error; the commands come
// first, under step 1, whose "operator key" is bold, the document only in
// them, and their ⧉ copy is named for what it copies.
func TestPageDeclareBuildsTheDocument(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
		e.expectValidatorReport("charlie")
		declareOpenForm(e)
		e.must(e.expect(declareBuild).ToHaveText("Continue"))
		e.must(e.expect(declarePin).ToBeHidden())
		e.must(e.expect(declareCountry + " option:checked").ToHaveText("— country —"))

		declareClickMap(e, 103.8+180, 90-1.3)
		lat, lon := declareCoordinate(e, declareLat), declareCoordinate(e, declareLon)
		if math.Abs(lat-1.3) > 1 || math.Abs(lon-103.8) > 1 {
			t.Errorf("a click on the map at Singapore (1.3, 103.8) set the fields to %v, %v", lat, lon)
		}
		declarePinAt(e, lat, lon)

		declareSetField(e, declareLat, "1.26")
		e.must(e.expect(declareLat).ToHaveValue("1.3"))
		declareSetField(e, declareLon, "103.84")
		e.must(e.expect(declareLon).ToHaveValue("103.8"))
		declarePinAt(e, 1.3, 103.8)
		declareSelectCountry(e, "SG")
		e.must(e.expect(declareCountry + " option:checked").ToHaveText("Singapore"))
		declareOtherProvider(e, "ovh")
		doc := declareBuildDocument(e)
		e.must(e.expect(declareOther).ToHaveValue("OVH"))
		e.must(e.expect(declareStatus).ToHaveText("document built: sign it with the commands below"))
		e.must(e.expect(declareStatus + ".err").ToHaveCount(0))

		op := keyAddress(declOpCharlie)
		declareExpectDocument(e, doc, location.Record{
			Chain: pageChainID, Operator: op, Lat: 1.3, Lon: 103.8, Country: "SG", Provider: "OVH", At: pageNow.Truncate(time.Second),
		})
		snippet := "command \\cat >| gnockpit-location.json <<'EOF'\n" + doc + "\nEOF\n" +
			"gnokey sign -tx-path gnockpit-location.json -chainid gnockpit-location -account-number 0 -account-sequence 0 " + op +
			" && command \\cat gnockpit-location.json"
		if got := declareTextOf(e, declareSnippet); got != snippet {
			t.Errorf("the snippet =\n%s\nwant\n%s", got, snippet)
		}
		e.must(e.expect(declareSection).ToContainText("1. Sign it with gnokey, in bash or zsh, on the machine whose gnokey holds the operator key:"))
		e.must(e.expect(declareSection).ToContainText("2. Paste the signed document gnokey printed:"))
		e.must(e.expect(declareSubmit).ToHaveText("Submit"))

		// The commands come first, under step 1, with their ⧉ copy: the
		// document shows only in them.
		e.must(e.expect(declareSigning + " > :first-child").ToHaveText("1. Sign it with gnokey, in bash or zsh, on the machine whose gnokey holds the operator key:"))
		e.must(e.expect(declareSigning + " > :nth-child(2) > button").ToHaveAccessibleName("copy the commands that sign it"))
		e.must(e.expect(declareSigning + " > :nth-child(3)").ToHaveClass("decl-snippet"))
		e.must(e.expect(declareSigning + " pre").ToHaveCount(1))
		e.must(e.expect(declareSigning + " button").ToHaveCount(2))
		// Step 1 reads as it is written, its "operator key" bold.
		const step1 = declareSigning + " > p.decl-step"
		if got, want := declareTextOf(e, step1), "1. Sign it with gnokey, in bash or zsh, on the machine whose gnokey holds the operator key:"; got != want {
			t.Errorf("step 1 = %q, want %q", got, want)
		}
		e.must(e.expect(step1 + " > strong").ToHaveText("operator key"))
		e.must(e.expect(step1 + " > strong").ToHaveCount(1))

		e.must(e.expect(declareCopySnippet).ToHaveText("⧉ copy"))
		e.press(e.Page.Locator(declareCopySnippet))
		expectToast(e, "Copied!")
		if p.browser == chromium {
			if got := e.clipboard(); got != snippet {
				t.Errorf("press %s: clipboard = %q, want %q", declareCopySnippet, got, snippet)
			}
		}
	})
}

// TestPageDeclareChecksTheProvider checks that Continue refuses a provider
// named under Other… that the server refuses, saying why in the server's
// words, as the browser says why a field is invalid: "--" holds no letter or
// digit, and no name, Not specified being the choice of none, is refused
// alike. A provider changed builds again.
func TestPageDeclareChecksTheProvider(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	declareFill(e, "1.3", "103.8", "SG", "")
	for _, name := range []string{"--", ""} {
		declareOtherProvider(e, name)
		e.press(e.Page.Locator(declareBuild))
		e.must(e.expect(declareOther).ToBeFocused())
		why, err := e.Page.Locator(declareOther).Evaluate(`(el) => el.validationMessage`, nil)
		e.must(err)
		if want := "provider must hold a letter or a digit"; why != want {
			t.Errorf("Other… named %q: the name field's validation message = %q, want %q", name, why, want)
		}
		e.must(e.expect(declareSigning).ToHaveCount(0))
	}

	declareSetField(e, declareOther, "OVH")
	doc := declareBuildDocument(e)
	declareExpectDocument(e, doc, location.Record{
		Chain: pageChainID, Operator: keyAddress(declOpCharlie), Lat: 1.3, Lon: 103.8, Country: "SG", Provider: "OVH", At: pageNow.Truncate(time.Second),
	})
}

// The form's map at points of the viewBox (x = lon + 180, y = 90 − lat)
// far from any border: inside Brazil (−10, −50), inside Australia (−25,
// 134), and at sea in the Atlantic (10, −40).
var (
	declareInBrazil    = [2]float64{-50 + 180, 90 + 10}
	declareInAustralia = [2]float64{134 + 180, 90 + 25}
	declareAtSea       = [2]float64{-40 + 180, 90 - 10}
)

// TestPageDeclareMapSetsTheCountry checks the country a point selected on
// the form's map gives, once the page has read the country borders: a click
// or a tap inside Brazil selects Brazil, saying nothing in the section's
// status; a point at sea keeps the country selected; a country selected by
// hand stands, typed coordinates leaving it as it is, until the next point
// on the map.
func TestPageDeclareMapSetsTheCountry(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
		e.expectValidatorReport("charlie")
		declareOpenForm(e)
		declareAwaitBorders(e)

		declareClickMap(e, declareInBrazil[0], declareInBrazil[1])
		e.must(e.expect(declareCountry).ToHaveValue("BR"))
		e.must(e.expect(declareStatus).ToBeEmpty())

		declareClickMap(e, declareAtSea[0], declareAtSea[1])
		if lat, lon := declareCoordinate(e, declareLat), declareCoordinate(e, declareLon); math.Abs(lat-10) > 1 || math.Abs(lon+40) > 1 {
			t.Errorf("a point at sea (10, -40) set the fields to %v, %v", lat, lon)
		}
		e.must(e.expect(declareCountry).ToHaveValue("BR"))

		declareSelectCountry(e, "PT")
		declareSetField(e, declareLat, "48.9")
		declareSetField(e, declareLon, "2.3")
		e.must(e.expect(declareCountry).ToHaveValue("PT"))

		declareClickMap(e, declareInAustralia[0], declareInAustralia[1])
		e.must(e.expect(declareCountry).ToHaveValue("AU"))
	})
}

// TestPageDeclareReadsTheBordersOnce checks the read of the country borders:
// the form's first opening starts it, and the form does not wait for it, a
// point selected meanwhile setting the coordinates alone; once they are read,
// a point selects its country. Opening the form again, or on the validator's
// page opened again, reads them no more.
func TestPageDeclareReadsTheBordersOnce(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
	var reads atomic.Int32
	e.Page.OnRequest(func(r playwright.Request) {
		if pathOf(r.URL()) == declareBordersPath {
			reads.Add(1)
		}
	})
	e.expectValidatorReport("charlie")
	read := e.hold(declareBordersPath)
	declareOpenForm(e)
	e.must(e.gate.awaitHeld(declareBordersPath, 1))
	declareClickMap(e, declareInBrazil[0], declareInBrazil[1])
	if lat, lon := declareCoordinate(e, declareLat), declareCoordinate(e, declareLon); math.Abs(lat+10) > 1 || math.Abs(lon+50) > 1 {
		t.Errorf("a point in Brazil (-10, -50) set the fields to %v, %v", lat, lon)
	}
	e.must(e.expect(declareCountry).ToHaveValue(""))

	read.release()
	declareAwaitBorders(e)
	declareClickMap(e, declareInBrazil[0], declareInBrazil[1])
	e.must(e.expect(declareCountry).ToHaveValue("BR"))

	e.press(e.Page.Locator(declareOpen))
	e.press(e.Page.Locator("#val-close"))
	e.expectValidatorPageClosed()
	e.press(e.validatorNameLink("charlie"))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	declareClickMap(e, declareInAustralia[0], declareInAustralia[1])
	e.must(e.expect(declareCountry).ToHaveValue("AU"))
	e.must(e.gate.settle())
	if n := reads.Load(); n != 1 {
		t.Errorf("the page read the country borders %d times, want once", n)
	}
}

// TestPageDeclareBordersUnread checks a form whose read of the country
// borders fails: the page warns in the console alone, the section's status
// empty, and a point selected on the map sets the coordinates, the country
// left to select by hand. The form's next opening reads them again, and a
// point then selects its country.
func TestPageDeclareBordersUnread(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()),
		expectConsole(`^warning: read the country borders: Failed to fetch$`))
	e.expectValidatorReport("charlie")
	lost := e.hold(declareBordersPath)
	declareOpenForm(e)
	lost.drop()
	lost.awaitFailure()
	declareClickMap(e, declareInBrazil[0], declareInBrazil[1])
	if lat, lon := declareCoordinate(e, declareLat), declareCoordinate(e, declareLon); math.Abs(lat+10) > 1 || math.Abs(lon+50) > 1 {
		t.Errorf("a point in Brazil (-10, -50) set the fields to %v, %v", lat, lon)
	}
	e.must(e.expect(declareCountry).ToHaveValue(""))
	e.must(e.expect(declareStatus).ToBeEmpty())

	again := e.hold(declareBordersPath)
	declareOpenForm(e)
	e.must(e.gate.awaitHeld(declareBordersPath, 1))
	again.release()
	declareAwaitBorders(e)
	declareClickMap(e, declareInBrazil[0], declareInBrazil[1])
	e.must(e.expect(declareCountry).ToHaveValue("BR"))
}

// TestPageDeclareBordersBound checks the bound of the country borders' read:
// one without an answer within 30 s is given up as one that failed, the page
// warning in the console alone, and a point selected on the map leaves the
// country to select by hand. The form's next opening reads them again.
func TestPageDeclareBordersBound(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()), withInstalledClock(),
		expectConsole(`^warning: read the country borders: no answer in 30 s$`))
	e.expectValidatorReport("charlie")
	stuck := e.hold(declareBordersPath)
	declareOpenForm(e)
	e.must(e.gate.awaitHeld(declareBordersPath, 1))
	e.runFor(30 * time.Second)
	stuck.awaitAbandoned()
	declareClickMap(e, declareInBrazil[0], declareInBrazil[1])
	e.must(e.expect(declareCountry).ToHaveValue(""))

	again := e.hold(declareBordersPath)
	declareOpenForm(e)
	e.must(e.gate.awaitHeld(declareBordersPath, 1))
	again.release()
	declareAwaitBorders(e)
	declareClickMap(e, declareInBrazil[0], declareInBrazil[1])
	e.must(e.expect(declareCountry).ToHaveValue("BR"))
}

// TestPageDeclareFormColumns checks the form's layout: the map beside the
// fields on a wide screen, above them on a phone.
func TestPageDeclareFormColumns(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
		e.expectValidatorReport("charlie")
		declareOpenForm(e)
		m, err := e.Page.Locator(declareMap).BoundingBox()
		e.must(err)
		f, err := e.Page.Locator(declareFields).BoundingBox()
		e.must(err)
		beside, below := f.X >= m.X+m.Width, f.Y >= m.Y+m.Height
		if wantBeside := p == desktop; beside != wantBeside || below == wantBeside {
			t.Errorf("the fields at %+v, the map at %+v: beside it %v, below it %v; want beside %v", *f, *m, beside, below, wantBeside)
		}
	})
}

// TestPageDeclareUsesTheServerClock checks that the record's time is the
// server's, whatever the browser's clock reads: 3 hours behind, a record
// timed on it would be 3 hours older than the server's time. The clock is
// the one the dashboard's last message gives: the browser's clock jumps 2
// hours ahead after the report came, and the next message keeps the record
// on the server's time.
func TestPageDeclareUsesTheServerClock(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()), withBrowserOffset(-3*time.Hour))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	e.must(e.Ctx.Clock().SetFixedTime(pageNow.Add(-time.Hour)))
	e.publish(declaredNetwork(t).snap)
	e.must(e.expect("#gc-drift").ToContainText("clock drift: 3600s"))
	declareFill(e, "1.3", "103.8", "SG", "")
	doc := declareBuildDocument(e)
	declareExpectDocument(e, doc, location.Record{
		Chain: pageChainID, Operator: keyAddress(declOpCharlie), Lat: 1.3, Lon: 103.8, Country: "SG", At: pageNow.Truncate(time.Second),
	})
}

// TestPageDeclareUsesTheReportsClock checks the record's time before the
// dashboard's first message, which carries the server's clock, has come:
// the report's time is the server's, and the record's is too, 3 hours ahead
// of the browser's clock.
func TestPageDeclareUsesTheReportsClock(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), noNavigate(), withBrowserOffset(-3*time.Hour))
	e.hold("/ws")
	e.navigate("/?val=" + valCharlie.address())
	e.expectValidatorReport("charlie")
	e.press(e.Page.Locator(declareOpen))
	declareFill(e, "1.3", "103.8", "SG", "")
	doc := declareBuildDocument(e)
	declareExpectDocument(e, doc, location.Record{
		Chain: pageChainID, Operator: keyAddress(declOpCharlie), Lat: 1.3, Lon: 103.8, Country: "SG", At: pageNow.Truncate(time.Second),
	})
	e.must(e.expect("#d-network").Not().ToHaveText(pageChainID))
}

// TestPageDeclareProviders checks the options of the provider select: Not
// specified, On premise, the listed providers, sorted (the peers' and the
// shown declarations'), then Other….
func TestPageDeclareProviders(t *testing.T) {
	none, onPremise, other := declareOption{"Not specified", ""}, declareOption{"On premise", location.OnPremise}, declareOption{"Other…", "other"}
	t.Run("peers", func(t *testing.T) {
		e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
		e.expectValidatorReport("charlie")
		declareOpenForm(e)
		declareExpectProviders(e, none, onPremise, declareOption{"AWS", "AWS"}, declareOption{"Hetzner", "Hetzner"}, declareOption{"OVH", "OVH"}, other)
	})
	t.Run("declared", func(t *testing.T) {
		sc := declaredNetwork(t)
		r := charlieInSingapore(t).Record
		r.Provider = "Scaleway"
		sc.declarations = []location.Declaration{declaration(t, declOpCharlie, r)}
		e := openPage(t, desktop, sc, withURL("/?val="+valCharlie.address()))
		e.expectValidatorReport("charlie")
		declareOpenForm(e)
		declareExpectProviders(e, none, onPremise, declareOption{"AWS", "AWS"}, declareOption{"Hetzner", "Hetzner"}, declareOption{"OVH", "OVH"},
			declareOption{"Scaleway", "Scaleway"}, other)
	})
}

// TestPageDeclareProvidersFollowTheMessages checks that the provider select
// lists the providers of the dashboard's last message each time the form
// opens, keeping the provider chosen: one the last message does not list as
// its name under Other….
func TestPageDeclareProvidersFollowTheMessages(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	declareSelectProvider(e, "AWS")

	// us-node, AWS's only peer, at Scaleway.
	snap := declaredNetwork(t).snap
	i := slices.IndexFunc(snap.Peers, func(p node.Peer) bool { return p.Moniker == "us-node" })
	if i < 0 {
		t.Fatal("declaredNetwork has no peer named us-node")
	}
	snap.Peers[i].Provider = "Scaleway"
	e.publish(snap)
	e.must(e.expect(peerEntry(desktop, "us-node")).ToContainText("Scaleway"))
	e.press(e.Page.Locator(declareOpen))
	declareExpectProviders(e, declareOption{"Not specified", ""}, declareOption{"On premise", location.OnPremise},
		declareOption{"Hetzner", "Hetzner"}, declareOption{"OVH", "OVH"}, declareOption{"Scaleway", "Scaleway"}, declareOption{"Other…", "other"})
	e.must(e.expect(declareProvider + " option:checked").ToHaveText("Other…"))
	e.must(e.expect(declareOther).ToHaveValue("AWS"))

	declareSelectProvider(e, "Hetzner")
	e.publish(declaredNetwork(t).snap)
	e.must(e.expect(peerEntry(desktop, "us-node")).ToContainText("AWS"))
	e.press(e.Page.Locator(declareOpen))
	declareExpectProviders(e, declareOption{"Not specified", ""}, declareOption{"On premise", location.OnPremise},
		declareOption{"AWS", "AWS"}, declareOption{"Hetzner", "Hetzner"}, declareOption{"OVH", "OVH"}, declareOption{"Other…", "other"})
	e.must(e.expect(declareProvider).ToHaveValue("Hetzner"))
	e.must(e.expect(declareOther).ToBeHidden())
}

// TestPageDeclareLeavesOutARefusedSpelling checks a peer's provider the
// record refuses, its name holding a soft hyphen, a format character, as an
// AS organization's may: the provider select does not offer it, and a name of
// its key under Other… keeps its own spelling.
func TestPageDeclareLeavesOutARefusedSpelling(t *testing.T) {
	sc := declaredNetwork(t)
	i := slices.IndexFunc(sc.snap.Peers, func(p node.Peer) bool { return p.Moniker == "us-node" })
	if i < 0 {
		t.Fatal("declaredNetwork has no peer named us-node")
	}
	sc.snap.Peers[i].Provider = "Soft\u00adHyphen Net"
	e := openPage(t, desktop, sc, withURL("/?val="+valCharlie.address()))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	e.must(e.expect(peerEntry(desktop, "us-node")).ToContainText("Soft\u00adHyphen Net"))
	declareExpectProviders(e, declareOption{"Not specified", ""}, declareOption{"On premise", location.OnPremise},
		declareOption{"Hetzner", "Hetzner"}, declareOption{"OVH", "OVH"}, declareOption{"Other…", "other"})
	declareFill(e, "1.3", "103.8", "SG", "")
	declareOtherProvider(e, "Soft Hyphen Net")
	doc := declareBuildDocument(e)
	e.must(e.expect(declareOther).ToHaveValue("Soft Hyphen Net"))
	declareExpectDocument(e, doc, location.Record{
		Chain: pageChainID, Operator: keyAddress(declOpCharlie), Lat: 1.3, Lon: 103.8, Country: "SG", Provider: "Soft Hyphen Net", At: pageNow.Truncate(time.Second),
	})
}

// TestPageDeclareOtherProvider checks a provider the select does not list:
// Other… reveals a field for its name, typed on the phone's keyboard too,
// which the document carries. Another choice hides the field, and the
// document carries that choice, not the name.
func TestPageDeclareOtherProvider(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
		e.expectValidatorReport("charlie")
		declareOpenForm(e)
		e.must(e.expect(declareOther).ToBeHidden())
		declareFill(e, "1.3", "103.8", "SG", "")
		declareChooseOther(e)
		e.must(e.expect(declareOther).ToBeVisible())
		e.must(e.expect(declareOther).ToHaveAccessibleName("Provider name"))
		e.press(e.Page.Locator(declareOther))
		e.must(e.Page.Keyboard().Type("Acme Cloud"))
		r := location.Record{
			Chain: pageChainID, Operator: keyAddress(declOpCharlie), Lat: 1.3, Lon: 103.8, Country: "SG", Provider: "Acme Cloud", At: pageNow.Truncate(time.Second),
		}
		declareExpectDocument(e, declareBuildDocument(e), r)

		declareSelectProvider(e, "AWS")
		e.must(e.expect(declareOther).ToBeHidden())
		r.Provider = "AWS"
		declareExpectDocument(e, declareBuildDocument(e), r)
	})
}

// TestPageDeclareUpdatesAnUnlistedProvider checks Update with a declared
// provider the page does not list: before the dashboard's first message,
// which lists the providers, the form opens with Other… chosen and the
// declared provider as its name.
func TestPageDeclareUpdatesAnUnlistedProvider(t *testing.T) {
	sc := declaredNetwork(t)
	sc.declarations = []location.Declaration{charlieInSingapore(t)}
	e := openPage(t, desktop, sc, noNavigate())
	e.hold("/ws")
	e.navigate("/?val=" + valCharlie.address())
	e.expectValidatorReport("charlie")
	e.press(e.Page.Locator(declareOpen))
	declareExpectProviders(e, declareOption{"Not specified", ""}, declareOption{"On premise", location.OnPremise}, declareOption{"Other…", "other"})
	e.must(e.expect(declareProvider + " option:checked").ToHaveText("Other…"))
	e.must(e.expect(declareOther).ToBeVisible())
	e.must(e.expect(declareOther).ToHaveValue("OVH"))
	e.must(e.expect("#d-network").Not().ToHaveText(pageChainID))
}

// TestPageDeclareSubmit checks a declaration submitted from the page: the
// document signed by charlie's operator, pasted with the newlines a terminal
// selection holds, is stored; the page reads the report again, says the
// declaration is stored, focuses Update and shows its status line, which
// holds the declared location; the map places charlie in Singapore once the
// server publishes. Closing the page takes the note away.
func TestPageDeclareSubmit(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
		e.expectValidatorReport("charlie")
		declareOpenForm(e)
		declareFill(e, "1.3", "103.8", "SG", "OVH")
		declareBuildDocument(e)
		declareSubmitSigned(e, declOpCharlie)

		where := e.validatorStatusSpans().Last()
		e.must(e.Expect.Locator(where).ToHaveText("🇸🇬 Singapore · OVH · 1.3, 103.8 · declared by the operator on " + pageNow.Format(time.DateOnly) + "ⓘ"))
		e.must(e.expect(declareNote).ToHaveText("declaration stored"))
		e.must(e.expect(declareOpen).ToHaveText("Update"))
		e.must(e.expect(declareOpen).ToBeFocused())
		declareExpectInDialog(e, "#val-note")
		declareExpectInDialog(e, "#val-body .val-status")
		e.must(e.expect(declareSigning).ToHaveCount(0))

		sg := mapDots + `.is-val[data-cc="SG"]`
		e.must(e.expect(sg).ToHaveCount(0))
		e.publish(declaredNetwork(t).snap)
		e.must(e.expect(sg).ToHaveCount(1))

		e.press(e.Page.Locator("#val-close"))
		e.expectValidatorPageClosed()
		e.must(e.expect(declareNote).ToBeEmpty())
	})
}

// TestPageDeclareNoteGoesWithTheNextSubmission checks that the note of a
// stored declaration goes as the next submission starts: that one refused,
// no note says a declaration is stored.
func TestPageDeclareNoteGoesWithTheNextSubmission(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()), expectStatus("/api/location", http.StatusBadRequest))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	declareFill(e, "1.3", "103.8", "SG", "OVH")
	declareBuildDocument(e)
	declareSubmitSigned(e, declOpCharlie)
	e.must(e.expect(declareNote).ToHaveText("declaration stored"))

	e.press(e.Page.Locator(declareOpen))
	declareSetField(e, declareLat, "1.4")
	declareBuildDocument(e)
	declareSubmitSigned(e, declOpBravo)
	e.must(e.expect(declareResult).ToHaveText("the signature's key is " + keyAddress(declOpBravo) + "'s, not the operator's (" + keyAddress(declOpCharlie) + ")"))
	e.must(e.expect(declareNote).ToBeEmpty())
}

// TestPageDeclareStoredAnswerCut checks a declaration stored whose answer
// reaches the page cut short, no JSON: the page reads the report again all
// the same, and says the declaration is stored.
func TestPageDeclareStoredAnswerCut(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), noNavigate())
	front := declareBehindCutAnswer(e, "/api/location")
	e.navigateTo(front + "/?val=" + valCharlie.address())
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	declareFill(e, "1.3", "103.8", "SG", "OVH")
	declareBuildDocument(e)
	declareSubmitSigned(e, declOpCharlie)
	e.must(e.Expect.Locator(e.validatorStatusSpans().Last()).ToHaveText("🇸🇬 Singapore · OVH · 1.3, 103.8 · declared by the operator on " + pageNow.Format(time.DateOnly) + "ⓘ"))
	e.must(e.expect(declareNote).ToHaveText("declaration stored"))
	e.must(e.expect(declareOpen).ToBeFocused())
}

// TestPageDeclareStoredReadFails checks a declaration stored whose report
// the page fails to read again: it says the declaration is stored, and why
// it shows no report, the focus on the page's Close.
func TestPageDeclareStoredReadFails(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	declareFill(e, "1.3", "103.8", "SG", "OVH")
	declareBuildDocument(e)
	read := e.hold("/api/validator")
	declareSubmitSigned(e, declOpCharlie)
	e.must(e.gate.awaitHeld("/api/validator?val="+keyAddress(declOpCharlie), 1))
	read.drop()
	read.awaitFailure()
	e.must(e.expect("#val-body .val-msg").ToHaveText("declaration stored; could not load: Failed to fetch"))
	e.must(e.expect(declareNote).ToHaveText("declaration stored"))
	e.must(e.expect("#val-close").ToBeFocused())
}

// TestPageDeclareRefused checks a document the server refuses: the page
// shows its reason, and Submit, pressed from the keyboard, takes the focus
// again once the answer comes. Charlie's document signed with bravo's
// operator key.
func TestPageDeclareRefused(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()), expectStatus("/api/location", http.StatusBadRequest))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	declareFill(e, "1.3", "103.8", "SG", "OVH")
	declareBuildDocument(e)
	declarePasteSigned(e, declOpBravo)
	e.must(e.Page.Locator(declareSubmit).Focus())
	e.must(e.Page.Keyboard().Press("Enter"))
	e.must(e.expect(declareResult).ToHaveText("the signature's key is " + keyAddress(declOpBravo) + "'s, not the operator's (" + keyAddress(declOpCharlie) + ")"))
	e.must(e.expect(declareSubmit).ToBeEnabled())
	e.must(e.expect(declareSubmit).ToBeFocused())
}

// TestPageDeclareSubmitsOnce checks Submit while its document is on its way:
// a double click sends it once, Submit stays disabled until the answer, and
// a stored declaration says so, plainly, while the page reads the report
// again.
func TestPageDeclareSubmitsOnce(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	declareFill(e, "1.3", "103.8", "SG", "OVH")
	declareBuildDocument(e)
	declarePasteSigned(e, declOpCharlie)
	var posts atomic.Int32
	e.Page.OnRequest(func(r playwright.Request) {
		if u, err := url.Parse(r.URL()); err == nil && u.Path == "/api/location" {
			posts.Add(1)
		}
	})
	post := e.hold("/api/location")
	e.must(e.Page.Locator(declareSubmit).Dblclick())
	e.must(e.gate.awaitHeld("/api/location", 1))
	e.must(e.expect(declareSubmit).ToBeDisabled())

	reread := e.hold("/api/validator")
	post.release()
	e.must(e.gate.awaitHeld("/api/validator?val="+keyAddress(declOpCharlie), 1))
	e.must(e.expect(declareSection + " div.decl-result[role=status]").ToHaveText("stored; reading the page again…"))
	e.must(e.expect(declareResult).ToHaveCount(0))
	reread.release()
	e.must(e.expect(declareOpen).ToHaveText("Update"))
	e.must(e.gate.settle())
	if n := posts.Load(); n != 1 {
		t.Errorf("the page posted the document %d times, want once", n)
	}
}

// TestPageDeclareAnswerStaysWithItsPage checks a submission whose answer
// comes once its page is closed and another validator's opened: charlie's
// declaration, stored, leaves bravo's page as it is, with no note, the focus
// where it was and no read of its report again.
func TestPageDeclareAnswerStaysWithItsPage(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	declareFill(e, "1.3", "103.8", "SG", "OVH")
	declareBuildDocument(e)
	declarePasteSigned(e, declOpCharlie)
	result, err := e.Page.Locator(declareSection + " div.decl-result[role=status]").ElementHandle()
	e.must(err)
	// The handler runs on playwright-go's one dispatch goroutine, which a
	// blocked send would stall: a read past the buffer is counted instead,
	// and fails the test.
	const kept = 8
	reads := make(chan string, kept)
	var overflow atomic.Int32
	e.Page.OnRequest(func(r playwright.Request) {
		if pathOf(r.URL()) != "/api/validator" {
			return
		}
		select {
		case reads <- r.URL():
		default:
			overflow.Add(1)
		}
	})
	t.Cleanup(func() {
		if n := overflow.Load(); n > 0 {
			t.Errorf("%d reads of a validator's report past the %d kept", n, kept)
		}
	})
	nextRead := func(v pageValidator) {
		t.Helper()
		select {
		case u := <-reads:
			if !strings.Contains(u, v.address()) {
				t.Errorf("read %s, want %s's report", u, v.moniker)
			}
		case <-time.After(pageTimeout):
			t.Fatalf("no read of %s's report", v.moniker)
		}
	}

	post := e.hold("/api/location")
	e.press(e.Page.Locator(declareSubmit))
	e.must(e.gate.awaitHeld("/api/location", 1))
	e.press(e.Page.Locator("#val-close"))
	e.expectValidatorPageClosed()
	e.press(e.validatorNameLink("bravo"))
	e.expectValidatorReport("bravo")
	nextRead(valBravo)
	where := e.validatorStatusSpans().Last().Locator(".info-btn")
	e.must(where.Focus())

	post.release()
	// Charlie's section, which no page shows, says the declaration stored:
	// the answer has come, and the page has done with it what it does.
	_, err = e.Page.WaitForFunction(`(el) => el.textContent === 'stored; reading the page again…'`, result)
	e.must(err)
	e.must(e.expect(declareNote).ToBeEmpty())
	e.must(e.Expect.Locator(where).ToBeFocused())

	// The next read is alpha's: none came for bravo in between.
	e.press(e.Page.Locator("#val-close"))
	e.expectValidatorPageClosed()
	e.press(e.validatorNameLink("alpha"))
	e.expectValidatorReport("alpha")
	nextRead(valAlpha)
}

// TestPageDeclareUnavailable checks a submission gnockpit cannot decide on
// yet, answered 503: no validator set read. The page says nothing was
// stored and to try again shortly, Submit enabled again.
func TestPageDeclareUnavailable(t *testing.T) {
	e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()), expectStatus("/api/location", http.StatusServiceUnavailable))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	declareFill(e, "1.3", "103.8", "SG", "OVH")
	declareBuildDocument(e)
	setless := declaredNetwork(t).snap
	setless.Validators = nil
	e.publish(setless)
	declareSubmitSigned(e, declOpCharlie)
	e.must(e.expect(declareResult).ToHaveText("validator set unknown — not stored; try again shortly"))
	e.must(e.expect(declareSubmit).ToBeEnabled())
}

// TestPageDeclareSubmitFails checks a submission without the server's
// answer: the page says why. Unanswered within 10 s, the server may yet
// store it: the page says so, and how to tell.
func TestPageDeclareSubmitFails(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		opts       []pageOption
		// open opens charlie's page, its submission set to fail.
		open func(e *pageEnv)
		// submit submits the document, then makes the submission fail.
		submit func(e *pageEnv)
	}{
		{
			name: "lost", want: "could not submit: Failed to fetch",
			open: func(e *pageEnv) { e.navigate("/?val=" + valCharlie.address()) },
			submit: func(e *pageEnv) {
				lost := e.hold("/api/location")
				declareSubmitSigned(e, declOpCharlie)
				lost.drop()
				lost.awaitFailure()
			},
		},
		{
			name: "proxy error", want: "could not submit: HTTP 502",
			opts: []pageOption{expectStatus("/api/location", http.StatusBadGateway)},
			open: func(e *pageEnv) {
				front := e.behindProxy("/api/location", http.StatusBadGateway)
				e.hold("/api/location")
				e.navigateTo(front + "/?val=" + valCharlie.address())
			},
			submit: func(e *pageEnv) { declareSubmitSigned(e, declOpCharlie) },
		},
		{
			name: "no answer", want: "gnockpit did not answer within 10 s; the declaration may be stored: reopen this page to check",
			opts: []pageOption{withInstalledClock()},
			open: func(e *pageEnv) { e.navigate("/?val=" + valCharlie.address()) },
			submit: func(e *pageEnv) {
				stuck := e.hold("/api/location")
				declareSubmitSigned(e, declOpCharlie)
				e.must(e.gate.awaitHeld("/api/location", 1))
				e.runFor(10 * time.Second)
				stuck.awaitAbandoned()
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := openPage(t, desktop, declaredNetwork(t), append(tc.opts, noNavigate())...)
			tc.open(e)
			e.expectValidatorReport("charlie")
			declareOpenForm(e)
			declareFill(e, "1.3", "103.8", "SG", "OVH")
			declareBuildDocument(e)
			tc.submit(e)
			e.must(e.expect(declareResult).ToHaveText(tc.want))
		})
	}
}

// TestPageDeclareRemove checks the removal of a declared location: Remove
// closes the form and builds the document of the removal, which the section's
// status says, as no error, its steps under the status and above the signed
// document's toggle; Update closes the document in turn, and empties
// the status. Signed and submitted, the removal takes the declared location
// away, and the section offers a declaration again.
func TestPageDeclareRemove(t *testing.T) {
	sc := declaredNetwork(t)
	sc.declarations = []location.Declaration{charlieInSingapore(t)}
	e := openPage(t, desktop, sc, withURL("/?val="+valCharlie.address()))
	e.expectValidatorReport("charlie")
	declareOpenForm(e)
	e.press(e.Page.Locator(declareRemove))
	doc := declareDocument(e)
	e.must(e.expect(declareForm).ToHaveCount(0))
	declareExpectDocument(e, doc, location.Record{
		Chain: pageChainID, Operator: keyAddress(declOpCharlie), Clear: true, At: pageNow.Truncate(time.Second),
	})
	e.must(e.expect(declareStatus).ToHaveText("removal built: sign it with the commands below"))
	e.must(e.expect(declareStatus + ".err").ToHaveCount(0))
	// The steps follow the status, above the signed document's toggle,
	// which stays the section's last part.
	e.must(e.expect(declareStatus + " + div.decl-sign + details.decl-doc").ToHaveCount(1))
	e.must(e.expect(declareDocToggle).ToHaveCount(1))
	e.press(e.Page.Locator(declareOpen))
	e.must(e.expect(declareForm).ToBeVisible())
	e.must(e.expect(declareSigning).ToHaveCount(0))
	e.must(e.expect(declareStatus).ToBeEmpty())
	e.press(e.Page.Locator(declareRemove))

	declareSubmitSigned(e, declOpCharlie)
	e.must(e.Expect.Locator(e.validatorStatusSpans().Last()).ToHaveText("— · OVHⓘ"))
	e.must(e.expect(declareNote).ToHaveText("removal stored"))
	e.must(e.expect(declareOpen).ToHaveText("Declare its location"))
	e.must(e.expect(declareOpen).ToBeFocused())
	declareExpectInDialog(e, "#val-note")
	declareExpectInDialog(e, "#val-body .val-status")
	e.must(e.expect(declareRemove).ToHaveCount(0))
	e.must(e.expect(declareSigned).ToHaveCount(0))
}

// TestPageDeclareKeyboard checks the form from the keyboard: Enter on
// Declare its location opens the form with Latitude focused, Tab goes on
// through the fields to Continue, Enter on it builds the document,
// and Tab goes on through the commands' copy button, the paste field and
// Submit. Once
// declared, Tab goes from Update to Remove, then to the signed document's
// toggle, which Enter opens, then to the document's copy button.
func TestPageDeclareKeyboard(t *testing.T) {
	t.Run("declaring", func(t *testing.T) {
		e := openPage(t, desktop, declaredNetwork(t), withURL("/?val="+valCharlie.address()))
		e.expectValidatorReport("charlie")
		declareAwaitLive(e)
		kb := e.Page.Keyboard()
		e.must(e.Page.Locator(declareOpen).Focus())
		e.must(e.expect(declareOpen).ToBeFocused())
		e.must(kb.Press("Enter"))
		e.must(e.expect(declareForm).ToBeVisible())
		e.must(e.expect(declareLat).ToBeFocused())

		tab := func(selector string) {
			t.Helper()
			e.must(kb.Press("Tab"))
			e.must(e.expect(selector).ToBeFocused())
		}
		e.must(kb.Type("1.3"))
		tab(declareLon)
		e.must(kb.Type("103.8"))
		tab(declareCountry)
		declareSelectCountry(e, "SG")
		e.must(e.expect(declareCountry).ToBeFocused())
		tab(declareProvider)
		declareSelectProvider(e, "OVH")
		e.must(e.expect(declareProvider).ToBeFocused())
		tab(declareBuild)
		e.must(kb.Press("Enter"))
		doc := declareDocument(e)
		declareExpectDocument(e, doc, location.Record{
			Chain: pageChainID, Operator: keyAddress(declOpCharlie), Lat: 1.3, Lon: 103.8, Country: "SG", Provider: "OVH", At: pageNow.Truncate(time.Second),
		})

		tab(declareCopySnippet)
		tab(declarePaste)
		tab(declareSubmit)
	})
	t.Run("declared", func(t *testing.T) {
		sc := declaredNetwork(t)
		sc.declarations = []location.Declaration{charlieInSingapore(t)}
		e := openPage(t, desktop, sc, withURL("/?val="+valCharlie.address()))
		e.expectValidatorReport("charlie")
		e.must(e.Page.Locator(declareOpen).Focus())
		e.must(e.expect(declareOpen).ToBeFocused())
		e.must(e.Page.Keyboard().Press("Tab"))
		e.must(e.expect(declareRemove).ToBeFocused())
		e.must(e.Page.Keyboard().Press("Tab"))
		e.must(e.expect(declareDocToggle).ToBeFocused())
		e.must(e.Page.Keyboard().Press("Enter"))
		e.must(e.expect(declareSigned).ToBeVisible())
		e.must(e.Page.Keyboard().Press("Tab"))
		e.must(e.expect(declareCopy).ToBeFocused())
	})
}
