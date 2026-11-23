package main

import (
	"flag"
	"io"
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
