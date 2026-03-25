package icon_test

import (
	"bytes"
	"image/png"
	"strings"
	"testing"

	"github.com/gnoverse/gnockpit/web/icon"
)

func TestAbbrev(t *testing.T) {
	cases := []struct{ chain, want string }{
		{"gnoland1", "G1"},
		{"gnototo1", "G1"}, // same abbrev as gnoland1; colors differ via hash
		{"test12", "T12"},
		{"portal-loop", "PL"},
		{"staging-3", "S3"},
		{"gno-dev-2", "GD2"},
		{"localnet", "L"},
		{"node-a", "NA"},
		{"a", "A"},
		{"123", "12"},
		{"", "?"},
	}
	for _, c := range cases {
		if got := icon.Abbrev(c.chain); got != c.want {
			t.Errorf("Abbrev(%q) = %q, want %q", c.chain, got, c.want)
		}
	}
}

func TestColorDeterministic(t *testing.T) {
	c1 := icon.ChainColor("gnoland1")
	c2 := icon.ChainColor("gnoland1")
	if c1 != c2 {
		t.Errorf("ChainColor not deterministic: %+v vs %+v", c1, c2)
	}
}

func TestColorDiffers(t *testing.T) {
	// gnoland1 and gnototo1 both abbreviate to G1 but must have different colors
	c1 := icon.ChainColor("gnoland1")
	c2 := icon.ChainColor("gnototo1")
	if c1 == c2 {
		t.Errorf("gnoland1 and gnototo1 have same color: %+v", c1)
	}
}

func TestSVGContainsAbbrev(t *testing.T) {
	svg := icon.SVG("gnoland1", 64)
	if !strings.Contains(svg, "G1") {
		t.Errorf("SVG missing abbreviation G1:\n%s", svg)
	}
}

func TestSVGValid(t *testing.T) {
	svg := icon.SVG("test12", 192)
	n := len(svg)
	if n > 40 {
		n = 40
	}
	if !strings.HasPrefix(svg, "<svg") {
		t.Errorf("SVG does not start with <svg: %q", svg[:n])
	}
	if !strings.HasSuffix(strings.TrimSpace(svg), "</svg>") {
		t.Errorf("SVG does not end with </svg>")
	}
}

func TestPNGIsValidPNG(t *testing.T) {
	data, err := icon.PNG("gnoland1", 192)
	if err != nil {
		t.Fatalf("PNG error: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("invalid PNG output: %v", err)
	}
}

func TestPNGSize(t *testing.T) {
	data, err := icon.PNG("gnoland1", 192)
	if err != nil {
		t.Fatalf("PNG error: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if img.Bounds().Dx() != 192 || img.Bounds().Dy() != 192 {
		t.Errorf("expected 192x192, got %v", img.Bounds())
	}
}
