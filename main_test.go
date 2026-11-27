package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestReadHeaderTimeoutFlag checks --read-header-timeout: 10 s unless set, the
// duration it is set to, 0 for none, and a value that is no duration or a
// negative one refused, naming the flag.
func TestReadHeaderTimeoutFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want time.Duration
	}{
		{"default", nil, 10 * time.Second},
		{"set", []string{"--read-header-timeout", "3s"}, 3 * time.Second},
		{"none", []string{"--read-header-timeout=0"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("gnockpit", flag.ContinueOnError)
			registerFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("parse %q: %v", tc.args, err)
			}
			if flagReadHeaderTimeout != tc.want {
				t.Errorf("parse %q: --read-header-timeout = %s, want %s", tc.args, flagReadHeaderTimeout, tc.want)
			}
		})
	}

	for _, tc := range []struct{ name, value, why string }{
		{"not a duration", "soon", `time: invalid duration "soon"`},
		{"negative", "-1s", "-1s is below 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("gnockpit", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			registerFlags(fs)
			args := []string{"--read-header-timeout", tc.value}
			want := `invalid value "` + tc.value + `" for flag -read-header-timeout: ` + tc.why
			if err := fs.Parse(args); err == nil || err.Error() != want {
				t.Errorf("parse %q: error %v, want %q", args, err, want)
			}
		})
	}
}

const (
	rankingOpA = "g1g0yrmdhney8pcaf9rfut4604equz604pszdsyt"
	rankingOpB = "g15t7f9q6km3ldt885duwl8xu5dncs98528amk4f"
)

// TestRankingFlags checks --ranking, --ranking-exclude and
// --ranking-hide-excluded: off and empty unless set, the operators listed
// parsed, and an empty entry or a malformed address refused, naming the flag.
func TestRankingFlags(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		wantOn      bool
		wantExclude []string
		wantHide    bool
	}{
		{"defaults", nil, false, nil, false},
		{"on", []string{"--ranking", "--ranking-hide-excluded"}, true, nil, true},
		{"two operators", []string{"--ranking-exclude=" + rankingOpA + "," + rankingOpB}, false, []string{rankingOpA, rankingOpB}, false},
		{"spaces trimmed", []string{"--ranking-exclude= " + rankingOpA + " , " + rankingOpB}, false, []string{rankingOpA, rankingOpB}, false},
		{"duplicates kept", []string{"--ranking-exclude=" + rankingOpA + "," + rankingOpA}, false, []string{rankingOpA, rankingOpA}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("gnockpit", flag.ContinueOnError)
			registerFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("parse %q: %v", tc.args, err)
			}
			if flagRanking != tc.wantOn || !slices.Equal(flagRankingExclude, tc.wantExclude) || flagRankingHideExcluded != tc.wantHide {
				t.Errorf("parse %q: --ranking %v, --ranking-exclude %v, --ranking-hide-excluded %v; want %v, %v, %v",
					tc.args, flagRanking, []string(flagRankingExclude), flagRankingHideExcluded, tc.wantOn, tc.wantExclude, tc.wantHide)
			}
		})
	}

	for _, tc := range []struct{ name, value, why string }{
		{"empty entries", rankingOpA + ",,", "an empty entry"},
		{"blank value", "", "an empty entry"},
		{"not an address", "nope", `"nope" is no g1 address`},
		{"too short", rankingOpA[:39], `"` + rankingOpA[:39] + `" is no g1 address`},
		{"too long", rankingOpA + "q", `"` + rankingOpA + `q" is no g1 address`},
		{"outside the bech32 alphabet", "g1b" + rankingOpA[3:], `"g1b` + rankingOpA[3:] + `" is no g1 address`},
		{"a one-character typo", rankingOpA[:39] + "q", `"` + rankingOpA[:39] + `q" is no g1 address`},
		{"uppercase", strings.ToUpper(rankingOpA), `"` + strings.ToUpper(rankingOpA) + `" is no g1 address`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("gnockpit", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			registerFlags(fs)
			args := []string{"--ranking-exclude=" + tc.value}
			want := `invalid value "` + tc.value + `" for flag -ranking-exclude: ` + tc.why
			if err := fs.Parse(args); err == nil || err.Error() != want {
				t.Errorf("parse %q: error %v, want %q", args, err, want)
			}
		})
	}
}

// TestCheckRankingFlags checks that a ranking flag set without --ranking is
// refused, naming it, and that the flags are accepted with it on, or all off.
func TestCheckRankingFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		on      bool
		exclude operatorList
		hide    bool
		want    string
	}{
		{"all off", false, nil, false, ""},
		{"on", true, operatorList{rankingOpA}, true, ""},
		{"exclude without ranking", false, operatorList{rankingOpA}, false, "--ranking-exclude needs --ranking"},
		{"hide without ranking", false, nil, true, "--ranking-hide-excluded needs --ranking"},
		{"both without ranking", false, operatorList{rankingOpA}, true, "--ranking-exclude needs --ranking"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkRankingFlags(tc.on, tc.exclude, tc.hide)
			if got := fmt.Sprint(err); (err == nil && tc.want != "") || (err != nil && got != tc.want) {
				t.Errorf("checkRankingFlags(%v, %v, %v) = %v, want %q", tc.on, tc.exclude, tc.hide, err, tc.want)
			}
		})
	}
}

// TestCheckPublicURL checks that --public-url is empty or an absolute http(s)
// URL of at most 512 bytes with a host, and that anything else is refused,
// naming the flag.
func TestCheckPublicURL(t *testing.T) {
	long := "https://x.example/" + strings.Repeat("a", 512-len("https://x.example/"))
	for _, s := range []string{"", "https://gnockpit.example.com", "http://localhost:8080/", long} {
		if err := checkPublicURL(s); err != nil {
			t.Errorf("checkPublicURL(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range []string{
		"gnockpit.example.com", "ftp://x.example", "https://", "https://:8080",
		"https://x.example/a b", "https://x.example/a\tb", " https://x.example", "https://x.example:99999", "https://x.example:0", "https://x.example:",
	} {
		want := fmt.Sprintf(`--public-url %q: want an absolute http or https URL, such as https://gnockpit.example.com`, s)
		if err := checkPublicURL(s); err == nil || err.Error() != want {
			t.Errorf("checkPublicURL(%q) = %v, want %q", s, err, want)
		}
	}
	want := "--public-url is 513 bytes: want at most 512"
	if err := checkPublicURL(long + "a"); err == nil || err.Error() != want {
		t.Errorf("checkPublicURL(a URL of 513 bytes) = %v, want %q", err, want)
	}
}

// TestCheckDBPath checks that --db-path names a file, and that an empty one is
// refused, naming the flag.
func TestCheckDBPath(t *testing.T) {
	for _, s := range []string{"/tmp/gnockpit.db", "gnockpit.db"} {
		if err := checkDBPath(s); err != nil {
			t.Errorf("checkDBPath(%q) = %v, want nil", s, err)
		}
	}
	want := "--db-path must name a database file"
	if err := checkDBPath(""); err == nil || err.Error() != want {
		t.Errorf(`checkDBPath("") = %v, want %q`, err, want)
	}
}
