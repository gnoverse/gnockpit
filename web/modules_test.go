package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// moduleURL is the file URL of the page's module name (js/<name>), quoted
// as a JavaScript string for an import.
func moduleURL(t *testing.T, name string) string {
	t.Helper()
	file, err := filepath.Abs(filepath.Join("js", name))
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Quote((&url.URL{Scheme: "file", Path: filepath.ToSlash(file)}).String())
}

// requireNode fails t, naming what to install, when node is not on PATH: a
// test that needs it never passes or skips without it.
func requireNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node not found: install Node (web/.nvmrc)")
	}
}

// runNode runs script as an ES module in node and returns what it wrote
// to stdout and stderr; a script that fails fails the test.
func runNode(t *testing.T, script string) (stdout, stderr string) {
	t.Helper()
	requireNode(t)
	cmd := exec.Command("node", "--input-type=module")
	cmd.Stdin = strings.NewReader(script)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, errOut.String())
	}
	return out.String(), errOut.String()
}

// TestStartEachIsolatesFailures checks that startEach calls every start in
// turn: one that throws is logged with its feature's name and its error,
// and the starts after it still run.
func TestStartEachIsolatesFailures(t *testing.T) {
	stdout, stderr := runNode(t, `import { startEach } from `+moduleURL(t, "start.js")+`
const ran = []
startEach([
    ['first', () => ran.push('first')],
    ['second', () => {
        throw new Error('second broke')
    }],
    ['third', () => ran.push('third')],
])
console.log(JSON.stringify(ran))
`)
	if got, want := strings.TrimSpace(stdout), `["first","third"]`; got != want {
		t.Errorf("starts run = %s, want %s", got, want)
	}
	const logged = "second did not start: Error: second broke"
	if !strings.Contains(stderr, logged) {
		t.Errorf("stderr = %q, want it to hold %q", stderr, logged)
	}
}

// TestLookupsNameTheMissingElement checks the lookups of the elements the
// page cannot work without (dom.js), against a stub of the document: $
// returns the HTML element of an id, $svg the SVG element of an id and $in
// the HTML element a selector matches first; a missing one throws an Error
// naming it, and so does one of the other kind (an SVG element for $ and
// $in, an HTML one for $svg), saying so.
func TestLookupsNameTheMissingElement(t *testing.T) {
	stdout, _ := runNode(t, `class Element {}
class HTMLElement extends Element {}
class SVGElement extends Element {}
globalThis.HTMLElement = HTMLElement
globalThis.SVGElement = SVGElement
const popover = new HTMLElement()
const toast = new HTMLElement()
const mapPoints = new SVGElement()
const byID = new Map([
    ['popover', popover],
    ['map-points', mapPoints],
])
const bySelector = new Map([
    ['.copy-toast', toast],
    ['#map-points', new SVGElement()],
])
globalThis.document = {
    getElementById: (id) => byID.get(id) ?? null,
    querySelector: (selector) => bySelector.get(selector) ?? null,
}
// Imported once the stub is in place: dom.js reads document when called.
const { $, $in, $svg } = await import(`+moduleURL(t, "dom.js")+`)
const look = (lookup, want) => {
    try {
        const got = lookup()
        return got === want ? 'returned it' : 'returned ' + String(got)
    } catch (err) {
        return 'threw ' + (err instanceof Error ? 'an Error: ' : 'a non-Error: ') + err.message
    }
}
console.log(JSON.stringify({
    foundID: look(() => $('popover'), popover),
    missingID: look(() => $('no-such-element')),
    foundSelector: look(() => $in(document, '.copy-toast'), toast),
    missingSelector: look(() => $in(document, '.no-such-element')),
    notHTML: look(() => $in(document, '#map-points')),
    idNotHTML: look(() => $('map-points')),
    foundSVG: look(() => $svg('map-points'), mapPoints),
    missingSVG: look(() => $svg('no-such-element')),
    notSVG: look(() => $svg('popover')),
}))
`)
	var got map[string]string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("read the lookups' results %q: %v", stdout, err)
	}
	for _, tc := range []struct{ name, want string }{
		{"foundID", "returned it"},
		{"missingID", "threw an Error: missing #no-such-element"},
		{"foundSelector", "returned it"},
		{"missingSelector", "threw an Error: missing .no-such-element"},
		{"notHTML", "threw an Error: #map-points is no HTML element"},
		{"idNotHTML", "threw an Error: #map-points is no HTML element"},
		{"foundSVG", "returned it"},
		{"missingSVG", "threw an Error: missing #no-such-element"},
		{"notSVG", "threw an Error: #popover is no SVG element"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got[tc.name] != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, got[tc.name], tc.want)
			}
		})
	}
}

// pageModules returns the paths of fsys's modules, its js/*.js files. A
// subdirectory of js is an error: the checks of the page's modules read
// js/*.js alone.
func pageModules(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, "js")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name := path.Join("js", e.Name())
		if e.IsDir() {
			return nil, fmt.Errorf("%s is a directory: the checks of the page's modules read js/*.js alone", name)
		}
		if strings.HasSuffix(name, ".js") {
			names = append(names, name)
		}
	}
	return names, nil
}

// moduleImports returns the static imports of fsys's modules (pageModules):
// for each module's path, the paths of the modules it imports. A dynamic
// import('./x.js') is not read: the page has none, and it does not order
// the modules' evaluation. An error names a module whose source the check
// cannot read as tokens, or which imports a specifier that may name a
// module of the page in another form than a sibling's, './<file>.js': one
// the browser resolves against the module (., / or ..), or one with an
// escape.
func moduleImports(fsys fs.FS) (map[string][]string, error) {
	names, err := pageModules(fsys)
	if err != nil {
		return nil, err
	}
	imports := make(map[string][]string, len(names))
	for _, name := range names {
		src, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		specs, err := importSpecifiers(string(src))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		imports[name] = []string{}
		for _, spec := range specs {
			if sibling.MatchString(spec) {
				imports[name] = append(imports[name], path.Join("js", spec))
			} else if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") || strings.Contains(spec, `\`) {
				return nil, fmt.Errorf("%s imports %q: the check reads a module of the page as './<file>.js' alone", name, spec)
			}
		}
	}
	return imports, nil
}

// sibling matches the specifier of a module beside the importing one,
// './<file>.js'.
var sibling = regexp.MustCompile(`^\./[\w.-]+\.js$`)

// jsKind is the kind of a jsToken.
type jsKind int

const (
	jsNone   jsKind = iota // before the first token
	jsWord                 // an identifier, a keyword or a number
	jsString               // a string literal
	jsValue                // a template literal or a regular expression
	jsPunct                // any other character
)

// jsToken is a token of a module's source as far as reading its imports
// needs: a word's text, a string literal's contents, or a punctuator.
type jsToken struct {
	kind jsKind
	text string
}

// importSpecifiers returns the specifiers of src's static imports, in the
// order they come: the string after from in an import or export
// declaration, and the one after import in a bare import, import './x.js'.
// It reads src as tokens, so no comment, string, template literal or
// regular expression is taken for an import.
func importSpecifiers(src string) ([]string, error) {
	toks, err := jsTokens(src)
	if err != nil {
		return nil, err
	}
	var specs []string
	for i := 1; i < len(toks); i++ {
		before := toks[i-1]
		if toks[i].kind != jsString || before.kind != jsWord {
			continue
		}
		member := i >= 2 && toks[i-2].text == "."
		if before.text == "from" || before.text == "import" && !member {
			specs = append(specs, toks[i].text)
		}
	}
	return specs, nil
}

// regexKeywords are the words after which a / starts a regular expression,
// as after an operator; after any other word, a number or a closing
// bracket, it divides.
var regexKeywords = []string{"await", "case", "delete", "do", "else", "in", "instanceof", "new", "of", "return", "throw", "typeof", "void", "yield"}

// jsTokens reads src as tokens, dropping comments and white space. A
// comment, a string, a template literal or a regular expression left open
// at the end of a line it cannot span, or of src, is an error naming its
// line.
func jsTokens(src string) ([]jsToken, error) {
	var toks []jsToken
	// open counts, for each template literal whose ${…} the reading is in,
	// the braces opened there and not closed.
	var open []int
	last := func() jsToken {
		if len(toks) == 0 {
			return jsToken{}
		}
		return toks[len(toks)-1]
	}
	unterminated := func(at int, what string) error {
		return fmt.Errorf("line %d: unterminated %s", strings.Count(src[:at], "\n")+1, what)
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case strings.HasPrefix(src[i:], "//"):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			i += end
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, unterminated(i, "comment")
			}
			i += 2 + end + 2
		case c == '\'' || c == '"':
			end := i + 1
			for end < len(src) && src[end] != c && src[end] != '\n' {
				if src[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(src) || src[end] != c {
				return nil, unterminated(i, "string")
			}
			toks = append(toks, jsToken{jsString, src[i+1 : end]})
			i = end + 1
		case c == '`' || c == '}' && len(open) > 0 && open[len(open)-1] == 0:
			// A template literal's text: from its start, or from the end
			// of a ${…} in it, to its end or to the next ${.
			if c == '}' {
				open = open[:len(open)-1]
			}
			end := i + 1
			for end < len(src) && src[end] != '`' && !strings.HasPrefix(src[end:], "${") {
				if src[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(src) {
				return nil, unterminated(i, "template literal")
			}
			if src[end] == '`' {
				toks = append(toks, jsToken{kind: jsValue})
				i = end + 1
			} else {
				open = append(open, 0)
				i = end + 2
			}
		case c == '/' && regexStarts(last()):
			end := i + 1
			for class := false; end < len(src) && (class || src[end] != '/') && src[end] != '\n'; end++ {
				switch src[end] {
				case '\\':
					end++
				case '[':
					class = true
				case ']':
					class = false
				}
			}
			if end >= len(src) || src[end] != '/' {
				return nil, unterminated(i, "regular expression")
			}
			for end++; end < len(src) && isWordByte(src[end]); end++ {
			}
			toks = append(toks, jsToken{kind: jsValue})
			i = end
		case isWordByte(c):
			end := i
			for end < len(src) && isWordByte(src[end]) {
				end++
			}
			toks = append(toks, jsToken{jsWord, src[i:end]})
			i = end
		default:
			if len(open) > 0 {
				switch c {
				case '{':
					open[len(open)-1]++
				case '}':
					open[len(open)-1]--
				}
			}
			toks = append(toks, jsToken{jsPunct, string(c)})
			i++
		}
	}
	if len(open) > 0 {
		return nil, unterminated(len(src), "template literal")
	}
	return toks, nil
}

// regexStarts tells whether a / after tok starts a regular expression: it
// does where an expression starts, it divides after one.
func regexStarts(tok jsToken) bool {
	switch tok.kind {
	case jsNone:
		return true
	case jsWord:
		return slices.Contains(regexKeywords, tok.text)
	case jsPunct:
		return !strings.Contains(")]}", tok.text)
	}
	return false
}

// isWordByte tells whether c may be part of a word: an identifier, a
// keyword or a number. A byte of a non-ASCII character counts, as such a
// character may be part of an identifier.
func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// importCycle returns a cycle among imports, as moduleImports gives them,
// each module named by its path, the first one again at its end; nil when
// they import in no cycle.
func importCycle(imports map[string][]string) []string {
	names := slices.Sorted(maps.Keys(imports))

	// A depth-first walk: a module met again while its own imports are
	// being walked closes a cycle, the modules on the path from it.
	const (
		unseen = iota
		walking
		walked
	)
	state := make(map[string]int, len(names))
	var trail []string
	var walk func(name string) []string
	walk = func(name string) []string {
		switch state[name] {
		case walking:
			return append(slices.Clone(trail[slices.Index(trail, name):]), name)
		case walked:
			return nil
		}
		state[name] = walking
		trail = append(trail, name)
		for _, imported := range imports[name] {
			if cycle := walk(imported); cycle != nil {
				return cycle
			}
		}
		trail = trail[:len(trail)-1]
		state[name] = walked
		return nil
	}
	for _, name := range names {
		if cycle := walk(name); cycle != nil {
			return cycle
		}
	}
	return nil
}

// TestModulesFormNoCycle checks that the page's modules import one another
// in no cycle: a module evaluates after all it imports, which a cycle
// breaks for one of its modules.
func TestModulesFormNoCycle(t *testing.T) {
	// A cycle of a.js and b.js, b.js importing a.js.
	cycle := []string{"js/a.js", "js/b.js", "js/a.js"}
	importsA := "import { a } from './a.js'\n"
	for _, tc := range []struct {
		name    string
		fsys    fstest.MapFS
		want    []string // the cycle importCycle finds
		wantErr string   // what moduleImports' error says, when it fails
	}{{
		name: "a cycle through each import form",
		fsys: fstest.MapFS{
			"js/a.js": {Data: []byte("import {\n    b,\n} from './b.js'\nexport const a = 1\n")},
			"js/b.js": {Data: []byte("export * from './c.js'\nexport const b = 2\n")},
			"js/c.js": {Data: []byte("import './a.js'\n")},
		},
		want: []string{"js/a.js", "js/b.js", "js/c.js", "js/a.js"},
	}, {
		name: "a comment with an apostrophe in an import list",
		fsys: fstest.MapFS{
			"js/a.js": {Data: []byte("import {\n    b, // the page's b\n} from './b.js'\n")},
			"js/b.js": {Data: []byte(importsA)},
		},
		want: cycle,
	}, {
		name: "a comment with a double quote in an import list",
		fsys: fstest.MapFS{
			"js/a.js": {Data: []byte("import {\n    b, // the \"b\"\n} from './b.js'\n")},
			"js/b.js": {Data: []byte(importsA)},
		},
		want: cycle,
	}, {
		name: "a comment with a semicolon in an import list",
		fsys: fstest.MapFS{
			"js/a.js": {Data: []byte("import {\n    b, /* x; y */\n} from './b.js'\n")},
			"js/b.js": {Data: []byte(importsA)},
		},
		want: cycle,
	}, {
		name: "an import name in quotes",
		fsys: fstest.MapFS{
			"js/a.js": {Data: []byte("import { \"b\" as b } from './b.js'\n")},
			"js/b.js": {Data: []byte(importsA)},
		},
		want: cycle,
	}, {
		name: "a re-export after a regular expression holding quotes",
		fsys: fstest.MapFS{
			"js/a.js": {Data: []byte("const re = /['\"`]/g\nexport { b } from './b.js'\n")},
			"js/b.js": {Data: []byte(importsA)},
		},
		want: cycle,
	}, {
		name: "imports in comments, strings and templates",
		fsys: fstest.MapFS{
			"js/a.js": {Data: []byte("// import { b } from './b.js'\n/*\nimport './b.js'\n*/\nconst s = \"import './b.js'\"\nconst t = `from './b.js' ${s} from './b.js'`\n")},
			"js/b.js": {Data: []byte(importsA)},
		},
	}, {
		name: "a module of the page named from its parent",
		fsys: fstest.MapFS{
			"js/a.js": {Data: []byte("import { b } from '../js/b.js'\n")},
			"js/b.js": {Data: []byte(importsA)},
		},
		wantErr: `js/a.js imports "../js/b.js"`,
	}, {
		name: "a source the check cannot read",
		fsys: fstest.MapFS{
			"js/a.js": {Data: []byte("import { b } from './b.js'\nconst s = 'open\n")},
			"js/b.js": {Data: []byte(importsA)},
		},
		wantErr: "js/a.js: line 2: unterminated string",
	}, {
		name: "a subdirectory",
		fsys: fstest.MapFS{
			"js/a.js":     {Data: []byte("import { c } from './sub/c.js'\n")},
			"js/sub/c.js": {Data: []byte("import { a } from '../a.js'\n")},
		},
		wantErr: "js/sub is a directory",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			imports, err := moduleImports(tc.fsys)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("moduleImports error = %v, want one holding %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("moduleImports: %v", err)
			}
			if got := importCycle(imports); !slices.Equal(got, tc.want) {
				t.Errorf("importCycle = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("the page's modules", func(t *testing.T) {
		imports, err := moduleImports(content)
		if err != nil {
			t.Fatal(err)
		}
		// The page's own import forms are read: a named import, a
		// namespace import and one over several lines.
		for _, edge := range [][2]string{
			{"js/main.js", "js/start.js"},
			{"js/main.js", "js/live.js"},
			{"js/eta.js", "js/alerts.js"},
		} {
			if !slices.Contains(imports[edge[0]], edge[1]) {
				t.Errorf("%s imports %q, want %s among them", edge[0], imports[edge[0]], edge[1])
			}
		}
		if cycle := importCycle(imports); cycle != nil {
			t.Errorf("the page's modules import in a cycle: %s", strings.Join(cycle, " → "))
		}
	})
}

// TestPercentagesRoundDown checks the percentages the ranking writes
// rounded down: n of total to digits decimals, so a figure reads 99.90 %
// only at or past 99.9 %; and a share of 1 as a whole percentage, its
// float64 error absorbed first (0.29 × 100 is 28.999999999999996), a share
// one block short of 2,000,000 still 99.
func TestPercentagesRoundDown(t *testing.T) {
	stdout, _ := runNode(t, `import { pctDown, sharePctDown } from `+moduleURL(t, "format.js")+`
console.log(JSON.stringify({
    pct: [pctDown(19979, 20000, 2), pctDown(999, 1000, 2), pctDown(6447, 6451, 2), pctDown(1, 1, 2)],
    share: [0.29, 0.57, 0.76875, 0.9999995, 0.999999, 1, 0].map(sharePctDown),
}))
`)
	var got struct{ Pct, Share []float64 }
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("read the percentages %q: %v", stdout, err)
	}
	if want := []float64{99.89, 99.9, 99.93, 100}; !slices.Equal(got.Pct, want) {
		t.Errorf("pctDown of 19979/20000, 999/1000, 6447/6451, 1/1 = %v, want %v", got.Pct, want)
	}
	if want := []float64{29, 57, 76, 99, 99, 100, 0}; !slices.Equal(got.Share, want) {
		t.Errorf("sharePctDown of 0.29, 0.57, 0.76875, 0.9999995, 0.999999, 1, 0 = %v, want %v", got.Share, want)
	}
}

// TestSubScoresRoundDown checks a ranking sub-score written to 3 decimals,
// rounded down, so 1.000 reads only at full marks, its float64 error
// absorbed first: 0.7 − 0.6 is 0.09999999999999998, written 0.100.
func TestSubScoresRoundDown(t *testing.T) {
	stdout, _ := runNode(t, `import { subScoreText } from `+moduleURL(t, "format.js")+`
console.log(JSON.stringify([0.9995, 0.864, 1, 0, 0.7 - 0.6].map(subScoreText)))
`)
	var got []string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("read the sub-scores %q: %v", stdout, err)
	}
	if want := []string{"0.999", "0.864", "1.000", "0.000", "0.100"}; !slices.Equal(got, want) {
		t.Errorf("subScoreText of 0.9995, 0.864, 1, 0, 0.7 − 0.6 = %q, want %q", got, want)
	}
}

// TestSharesWhereLowerIsBetterRoundUp checks a missed share written
// rounded up, n of total to digits decimals from the exact counts, so it
// never reads better than its value: a miss never reads 0.00 %, and 4.95 %
// to a tenth reads 5.0 %.
func TestSharesWhereLowerIsBetterRoundUp(t *testing.T) {
	stdout, _ := runNode(t, `import { pctUp } from `+moduleURL(t, "format.js")+`
console.log(JSON.stringify([pctUp(1, 26000, 2), pctUp(4950, 100000, 1), pctUp(1, 2001, 1), pctUp(1, 150, 2), pctUp(7, 100, 2), pctUp(0, 5, 2)]))
`)
	var got []float64
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("read the shares %q: %v", stdout, err)
	}
	if want := []float64{0.01, 5, 0.1, 0.67, 7, 0}; !slices.Equal(got, want) {
		t.Errorf("pctUp of 1/26,000, 4,950/100,000, 1/2,001, 1/150, 7/100, 0/5 = %v, want %v", got, want)
	}
}

// TestRecoveryTimesRoundUp checks a recovery time written rounded up at
// its last unit, so it never reads faster than it was: 600.4 s reads
// 10 min 01 s, past the 10 minutes of full marks; minutes from an hour,
// days and hours from 48 h, so 48 h and 1 ms reads 2 d 01 h.
func TestRecoveryTimesRoundUp(t *testing.T) {
	const ms = "[600400, 600000, 0, 59001, 3600001, 86399600, 172799000, 172800000, 172800001, 180000001, 259199000]"
	stdout, _ := runNode(t, `import { durationUpText } from `+moduleURL(t, "format.js")+`
console.log(JSON.stringify(`+ms+`.map(durationUpText)))
`)
	var got []string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("read the durations %q: %v", stdout, err)
	}
	want := []string{"10 min 01 s", "10 min 00 s", "0 s", "1 min 00 s", "1 h 01 min", "24 h 00 min", "2 d 00 h", "2 d 00 h", "2 d 01 h", "2 d 03 h", "3 d 00 h"}
	if !slices.Equal(got, want) {
		t.Errorf("durationUpText of %s ms = %q, want %q", ms, got, want)
	}
}

// TestScoresRoundDown checks a ranking score written to one decimal,
// rounded down, its float64 error absorbed first, so it never reads better
// than it is: 99.96 reads 99.9, never 100.0; 0.7 + 0.1, 0.7999999999999999,
// reads 0.8.
func TestScoresRoundDown(t *testing.T) {
	stdout, _ := runNode(t, `import { scoreText } from `+moduleURL(t, "format.js")+`
console.log(JSON.stringify([93.25, 99.96, 99.94999, 96.95, 100, 0, 0.7 + 0.1].map(scoreText)))
`)
	var got []string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("read the scores %q: %v", stdout, err)
	}
	if want := []string{"93.2", "99.9", "99.9", "96.9", "100.0", "0.0", "0.8"}; !slices.Equal(got, want) {
		t.Errorf("scoreText of 93.25, 99.96, 99.94999, 96.95, 100, 0, 0.7 + 0.1 = %q, want %q", got, want)
	}
}

// TestPointsRoundDown checks a part's points written to one decimal: its
// weight's text, a tie rounded up, at full marks; below them, rounded down
// and never the weight's text, one tenth less where rounding down reaches
// it (22.22 of 22.222… reads 22.1).
func TestPointsRoundDown(t *testing.T) {
	stdout, _ := runNode(t, `import { pointsText, weightText } from `+moduleURL(t, "format.js")+`
console.log(JSON.stringify({
    points: [
        pointsText({ sub: 1, weight: 50, points: 50 }),
        pointsText({ sub: 1, weight: 200 / 9, points: 200 / 9 }),
        pointsText({ sub: 1, weight: 500 / 9, points: 500 / 9 }),
        pointsText({ sub: 0.9999, weight: 200 / 9, points: (0.9999 * 200) / 9 }),
        pointsText({ sub: 0.9999, weight: 50, points: 49.995 }),
        pointsText({ sub: 0.9999, weight: 500 / 9, points: (0.9999 * 500) / 9 }),
        pointsText({ sub: 0.99968, weight: 10, points: 9.9968 }),
        pointsText({ sub: 0.666, weight: 62.5, points: 41.625 }),
        pointsText({ sub: 0, weight: 25, points: 0 }),
    ],
    weights: [50, 200 / 9, 500 / 9, 62.5, 12.5].map(weightText),
}))
`)
	var got struct{ Points, Weights []string }
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("read the points %q: %v", stdout, err)
	}
	if want := []string{"50.0", "22.2", "55.6", "22.1", "49.9", "55.5", "9.9", "41.6", "0.0"}; !slices.Equal(got.Points, want) {
		t.Errorf("pointsText = %q, want %q", got.Points, want)
	}
	if want := []string{"50.0", "22.2", "55.6", "62.5", "12.5"}; !slices.Equal(got.Weights, want) {
		t.Errorf("weightText of 50, 22.222…, 55.555…, 62.5, 12.5 = %q, want %q", got.Weights, want)
	}
}

// rankingStub is the document, the fetch and the timers the Ranking view
// runs on in node: the panel and the dialog's figures, elements that record
// their children and attributes, a fetch answering answer, and setTimeout
// recording each delay asked, running nothing; the observer and the media
// query the popover's module, which the view's explanations import, sets up
// as it loads; and the window's addEventListener, which takes the view's
// popstate listener and never calls it.
const rankingStub = `class Element {}
class HTMLElement extends Element {
    constructor(tag) {
        super()
        this.tag = tag
        this.kids = []
        this.style = {}
        this.attrs = {}
        this.textContent = ''
        this.className = ''
    }
    appendChild(child) {
        this.kids.push(child)
        return child
    }
    append(...children) {
        this.kids.push(...children)
    }
    replaceChildren(...children) {
        this.kids = children
    }
    setAttribute(name, value) {
        this.attrs[name] = value
    }
    removeAttribute(name) {
        delete this.attrs[name]
    }
}
globalThis.HTMLElement = HTMLElement
globalThis.MutationObserver = class {}
globalThis.window = { matchMedia: () => ({ matches: false }), addEventListener: () => {} }
const panel = new HTMLElement('div')
const figures = new Map(['ranking-window', 'ranking-heights', 'ranking-formula'].map((id) => [id, new HTMLElement('span')]))
globalThis.document = {
    getElementById: (id) => (id === 'validators-ranking' ? panel : (figures.get(id) ?? null)),
    createElement: (tag) => new HTMLElement(tag),
}
let answer = null
globalThis.fetch = async () => ({ ok: true, json: async () => answer })
const delays = []
globalThis.setTimeout = (fn, ms) => {
    delays.push(ms)
    return 0
}
const settle = () => new Promise((resolve) => setImmediate(resolve))
const text = (n) => (typeof n === 'string' ? n : n.textContent + n.kids.map(text).join(''))
`

// TestRankingShowsAnAnswerItCannotDraw checks the Ranking view on an
// answer that breaks the server's contract (an unavailable state without
// its floor): the panel says it could not show the ranking, and the next
// read is still asked for, 60 s later.
func TestRankingShowsAnAnswerItCannotDraw(t *testing.T) {
	stdout, _ := runNode(t, rankingStub+`const { start, rankingShown } = await import(`+moduleURL(t, "ranking.js")+`)
answer = {
    state: { kind: 'unavailable', stored_share: 0, left_s: 0, events_pending: false },
    hide_excluded: false,
    ranking: null,
}
start()
rankingShown()
await settle()
console.log(JSON.stringify({ shown: panel.kids.map(text), polls: delays.filter((ms) => ms === 60000).length }))
`)
	var got struct {
		Shown []string
		Polls int
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("read the panel %q: %v", stdout, err)
	}
	if want := []string{"Could not show the ranking (an indexed read found nothing)."}; !slices.Equal(got.Shown, want) {
		t.Errorf("panel = %q, want %q", got.Shown, want)
	}
	if got.Polls != 1 {
		t.Errorf("%d reads asked 60 s later, want 1", got.Polls)
	}
}

// TestRankingShowsAnOutagesPartWithoutSubScore checks the Ranking view on a
// ranking whose entry has an outages part without a sub-score, which the
// server always gives: the panel says it could not show the ranking,
// rather than write a sub-score it was not given.
func TestRankingShowsAnOutagesPartWithoutSubScore(t *testing.T) {
	stdout, _ := runNode(t, rankingStub+`const { start, rankingShown } = await import(`+moduleURL(t, "ranking.js")+`)
const none = { sub: null, weight: 0, points: 0 }
answer = {
    state: { kind: 'ready', stored_share: 1, left_s: 0, events_pending: false },
    hide_excluded: false,
    ranking: {
        chain: 'test-13', day: '2026-11-18', computed_at: '2026-11-18T00:01:00Z', exclusions: [], formula: 1,
        first_day: '2026-10-19', last_day: '2026-11-17', first_height: 1, last_height: 10, proposals_disabled: [],
        entries: [{
            operator: 'g1operator', name: 'alpha', keys: ['g1operator'], status: 'ranked', rank: 1, hours_in_set: 200,
            score: 100, uptime: { sub: 1, weight: 100, points: 100 }, outages: none, recovery: none, proposals: none,
            tied_with: [],
            inputs: {
                in_set: 10, recovering: 0, signed: 10, nil: 0, absent: 0, other_block: 0, outages: [], longest_absence: 0,
                recovery: [], recovery_median_ms: null, proposals_expected: 0, proposals_missed: 0,
            },
        }],
    },
}
start()
rankingShown()
await settle()
console.log(JSON.stringify(panel.kids.map(text)))
`)
	var shown []string
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatalf("read the panel %q: %v", stdout, err)
	}
	if want := []string{"Could not show the ranking (an indexed read found nothing)."}; !slices.Equal(shown, want) {
		t.Errorf("panel = %q, want %q", shown, want)
	}
}
