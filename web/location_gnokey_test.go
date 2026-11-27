package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// gnokeyMnemonic is gno's public test mnemonic; its key's address is
// gnokeyOperator.
const (
	gnokeyMnemonic = "source bonus chronic canvas draft south burst lottery vacant surface solve popular case indicate oppose farm nothing bullet exhibit title speed wink action roast"
	gnokeyOperator = "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"
)

// TestLocationSignedByGnokey checks the page's document end to end with
// each gnokey binary GNOCKPIT_GNOKEY lists (see gnokeyBinaries), which make
// test-gnokey builds: gnokey signs the document location-record.js writes,
// with the flags of the page's snippet, and POST /api/location stores the
// location it carries. Without GNOCKPIT_GNOKEY the test skips; set, it fails
// unless it lists a binary.
func TestLocationSignedByGnokey(t *testing.T) {
	value, set := os.LookupEnv("GNOCKPIT_GNOKEY")
	binaries, err := gnokeyBinaries(value, set)
	if err != nil {
		t.Fatal(err)
	}
	if len(binaries) == 0 {
		t.Skip("GNOCKPIT_GNOKEY is not set: make test-gnokey builds the gnokey binaries and runs this test")
	}
	for _, gnokey := range binaries {
		t.Run(filepath.Base(filepath.Dir(gnokey)), func(t *testing.T) {
			home, dir := t.TempDir(), t.TempDir()
			runCommand(t, dir, gnokeyMnemonic+"\npw\npw\n", gnokey,
				"add", "-home", home, "-insecure-password-stdin", "-recover", "op")

			snippet := runRecord(t, `const op = `+strconv.Quote(gnokeyOperator)+`
const r = {
    chain: `+strconv.Quote(locationChainID)+`, operator: op, lat: 1.3, lon: 103.8, country: 'SG', provider: 'ovh sas',
    at: rec.recordTime(`+strconv.FormatInt(seamNow.UnixMilli(), 10)+`),
}
process.stdout.write(rec.signSnippet(rec.unsignedDocument(rec.locationMemo(r), op), op))
`)
			// The lines before the sign command write the document, run by a
			// shell as the operator pastes them: over a file a former attempt
			// left, in a shell set to noclobber.
			file := filepath.Join(dir, "gnockpit-location.json")
			if err := os.WriteFile(file, []byte("a former attempt's document"), 0o600); err != nil {
				t.Fatal(err)
			}
			write := snippet[:strings.LastIndex(snippet, "\n")+1]
			runCommand(t, dir, "set -o noclobber\n"+write, "sh")
			runCommand(t, dir, "pw\n", gnokey, signArgs(t, snippet, home)...)

			signed, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			code, answer := postLocation(t, locationServerFor(t, gnokeyOperator), signed)
			want := map[string]any{
				"operator": gnokeyOperator, "country": "SG", "lat": 1.3, "lon": 103.8, "provider": "OVH SAS",
				"at": "2026-11-18T12:00:00Z",
			}
			if code != http.StatusOK || !reflect.DeepEqual(answer, want) {
				t.Errorf("POST /api/location of %s = %d %v, want 200 %v", signed, code, answer, want)
			}
		})
	}
}

// gnokeyBinaries returns the gnokey binaries GNOCKPIT_GNOKEY lists, given its
// value and whether it is set: separated as PATH is, an empty entry naming
// none. Unset, it lists none; set, it must list one, since make test-gnokey
// sets it to the binaries it built, and a list of none means it built none.
func gnokeyBinaries(value string, set bool) ([]string, error) {
	if !set {
		return nil, nil
	}
	binaries := slices.DeleteFunc(filepath.SplitList(value), func(s string) bool { return s == "" })
	if len(binaries) == 0 {
		return nil, fmt.Errorf("GNOCKPIT_GNOKEY is set and lists no gnokey binary (%q): make test-gnokey built none, or unset it to skip the test", value)
	}
	return binaries, nil
}

func TestGnokeyBinaries(t *testing.T) {
	sep := string(filepath.ListSeparator)
	for _, c := range []struct {
		name    string
		value   string
		set     bool
		want    []string
		wantErr bool
	}{
		{name: "unset"},
		{name: "set empty", set: true, wantErr: true},
		{name: "set to a separator", value: sep, set: true, wantErr: true},
		{name: "one binary", value: "/b/v1.1.0/gnokey", set: true, want: []string{"/b/v1.1.0/gnokey"}},
		{
			name: "two binaries, an empty entry between", value: "/b/v1.1.0/gnokey" + sep + sep + "/b/v1.7.1/gnokey", set: true,
			want: []string{"/b/v1.1.0/gnokey", "/b/v1.7.1/gnokey"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := gnokeyBinaries(c.value, c.set)
			if (err != nil) != c.wantErr || !slices.Equal(got, c.want) {
				t.Errorf("gnokeyBinaries(%q, %t) = %q, %v; want %q, an error %t", c.value, c.set, got, err, c.want, c.wantErr)
			}
		})
	}
}

// signArgs is the arguments of the gnokey sign command snippet runs (the
// command before its &&), with gnokey's home, the password read from stdin
// and no prompt added: the page's flags, run without a terminal.
func signArgs(t *testing.T, snippet, home string) []string {
	t.Helper()
	lines := strings.Split(snippet, "\n")
	command, _, found := strings.Cut(lines[len(lines)-1], " && ")
	args := strings.Fields(command)
	if !found || len(args) < 2 || args[0] != "gnokey" || args[1] != "sign" {
		t.Fatalf("the snippet's last line is no gnokey sign command and its &&:\n%s", snippet)
	}
	return append([]string{"sign", "-home", home, "-insecure-password-stdin", "-quiet"}, args[2:]...)
}

// commandWait bounds each command the test runs, so a command that waits
// on something the test never gives fails the test instead of holding it.
const commandWait = 30 * time.Second

// runCommand runs name with args in dir, stdin as its input, killed after
// commandWait; a failure fails the test with the command's output.
func runCommand(t *testing.T, dir, stdin, name string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), commandWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// A child of a killed command (a shell's) may hold its output open:
	// the wait for it ends a second after the kill.
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w, not done within %s", err, commandWait)
		}
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}
