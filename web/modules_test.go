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
