// Package icon generates chain-specific SVG and PNG icons.
package icon

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
)

// Color holds the three derived colors for a chain icon.
type Color struct {
	BG         string // hsl background
	Stroke     string // bright stroke (ECG bottom and mid)
	StrokeDark string // muted stroke (ECG top layer)
	HexBG      string // #rrggbb equivalent of BG (for manifest theme_color)
	Hue        int
}

// Abbrev returns the 1–3 char abbreviation for a chain name.
// Algorithm mirrors the JS preview: scan for runs of [a-zA-Z] and [0-9],
// take the first letter of each word run (max 2) then digits (max 2), cap at 3.
// Examples: gnoland1→G1, portal-loop→PL, gno-dev-2→GD2, test12→T12.
func Abbrev(chain string) string {
	if chain == "" {
		return "?"
	}
	var wordLetters []byte
	var digits []byte
	i := 0
	for i < len(chain) {
		b := chain[i]
		lower := b | 32
		if lower >= 'a' && lower <= 'z' {
			// word run: record first letter, skip the rest of the run
			if len(wordLetters) < 2 {
				wordLetters = append(wordLetters, lower-32) // uppercase
			}
			i++
			for i < len(chain) && (chain[i]|32) >= 'a' && (chain[i]|32) <= 'z' {
				i++
			}
		} else if b >= '0' && b <= '9' {
			// digit run: collect up to 2 digits
			for i < len(chain) && chain[i] >= '0' && chain[i] <= '9' {
				if len(digits) < 2 {
					digits = append(digits, chain[i])
				}
				i++
			}
		} else {
			i++
		}
	}
	result := string(wordLetters) + string(digits)
	if len(result) > 3 {
		result = result[:3]
	}
	if result == "" {
		return "?"
	}
	return result
}

// ChainColor derives deterministic icon colors from the full chain name.
func ChainColor(chain string) Color {
	// djb2 hash
	h := uint32(5381)
	for i := 0; i < len(chain); i++ {
		h = (h*33) ^ uint32(chain[i])
	}
	hue := int(h % 360)
	return Color{
		Hue:        hue,
		BG:         fmt.Sprintf("hsl(%d,55%%,26%%)", hue),
		Stroke:     fmt.Sprintf("hsl(%d,80%%,80%%)", hue),
		StrokeDark: fmt.Sprintf("hsl(%d,60%%,45%%)", hue),
		HexBG:      hslToHex(hue, 55, 26),
	}
}

// SVG returns the complete SVG icon markup at the given display size (px).
func SVG(chain string, size int) string {
	abbr := Abbrev(chain)
	col := ChainColor(chain)
	fs := fontSize(abbr)
	path := ecgPath()

	ecgBottom := fmt.Sprintf(
		`<path d=%q stroke=%q stroke-width="2.5" fill="none" stroke-linecap="round" stroke-linejoin="round"/>`,
		path, col.Stroke)

	// Text sits between the two ECG layers (layers 3 of 5).
	// paint-order="stroke fill" draws the black border behind the white fill.
	text := fmt.Sprintf(
		`<text x="32" y="33" dominant-baseline="central" text-anchor="middle" `+
			`font-family="-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif" `+
			`font-weight="800" font-size="%d" fill="white" `+
			`stroke="black" stroke-width="2" stroke-linejoin="round" paint-order="stroke fill">%s</text>`,
		fs, abbr)

	ecgMid := fmt.Sprintf(
		`<path d=%q stroke=%q stroke-width="2.5" fill="none" stroke-linecap="round" stroke-linejoin="round" opacity="0.5"/>`,
		path, col.Stroke)

	ecgTop := fmt.Sprintf(
		`<path d=%q stroke=%q stroke-width="0.2" fill="none" stroke-linecap="round" stroke-linejoin="round"/>`,
		path, col.StrokeDark)

	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" width="%d" height="%d">`+
			`<rect width="64" height="64" rx="13" fill=%q/>`+
			`%s%s%s%s`+
			`</svg>`,
		size, size, col.BG, ecgBottom, text, ecgMid, ecgTop)
}

func fontSize(abbr string) int {
	switch len(abbr) {
	case 1:
		return 42
	case 2:
		return 30
	default:
		return 22
	}
}

func ecgPath() string {
	return "M 5,32 L 18,32 L 22,22 L 26,40 L 30,32 L 59,32"
}

// hslToHex converts HSL (h 0-359, s 0-100, l 0-100) to #rrggbb.
func hslToHex(h, s, l int) string {
	r, g, b := hslToRGB(float64(h)/360, float64(s)/100, float64(l)/100)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	var r, g, b float64
	if s == 0 {
		r, g, b = l, l, l
	} else {
		q := l + s - l*s
		if l < 0.5 {
			q = l * (1 + s)
		}
		p := 2*l - q
		r = hueToRGB(p, q, h+1.0/3)
		g = hueToRGB(p, q, h)
		b = hueToRGB(p, q, h-1.0/3)
	}
	return uint8(r * 255), uint8(g * 255), uint8(b * 255)
}

// PNG renders the icon as a PNG image at the given size in pixels.
// Layer order matches SVG: bg → ECG bottom → text → ECG mid → ECG top.
// The text is sandwiched between the two ECG layers (intentional per design).
func PNG(chain string, size int) ([]byte, error) {
	abbr := Abbrev(chain)
	col := ChainColor(chain)
	sc := float64(size) / 64.0 // scale from 64x64 viewBox

	dc := gg.NewContext(size, size)

	// ---- Layer 1: Background
	r8, g8, b8 := hslToRGB(float64(col.Hue)/360, 0.55, 0.26)
	dc.SetColor(color.RGBA{R: r8, G: g8, B: b8, A: 255})
	dc.DrawRoundedRectangle(0, 0, float64(size), float64(size), 13*sc)
	dc.Fill()

	// ---- Layer 2: ECG bottom (solid)
	sr8, sg8, sb8 := hslToRGB(float64(col.Hue)/360, 0.80, 0.80)
	drawECG(dc, sc, color.RGBA{R: sr8, G: sg8, B: sb8, A: 255}, 2.5*sc)

	// ---- Layer 3: Text (white with black border via two-pass drawing)
	pts := float64(fontSize(abbr)) * sc
	face, err := loadBoldFace(pts)
	if err != nil {
		return nil, fmt.Errorf("load font: %w", err)
	}
	dc.SetFontFace(face)
	cx := float64(size) / 2

	// Compute baseline so the cap-height center aligns with SVG dominant-baseline="central" at y=33.
	// DrawStringAnchored with ay=0 treats y as the baseline.
	capH := float64(face.Metrics().CapHeight) / 64.0
	baseline := float64(size)/2 + sc + capH/2

	// Border pass: black text at 8 cardinal/diagonal offsets (1 canvas unit = sc px).
	bd := sc
	dc.SetColor(color.RGBA{R: 0, G: 0, B: 0, A: 200})
	for _, off := range [][2]float64{
		{-1, -1}, {0, -1}, {1, -1},
		{-1, 0}, {1, 0},
		{-1, 1}, {0, 1}, {1, 1},
	} {
		dc.DrawStringAnchored(abbr, cx+off[0]*bd, baseline+off[1]*bd, 0.5, 0)
	}
	// Fill pass: white text centered
	dc.SetColor(color.RGBA{R: 255, G: 255, B: 255, A: 255})
	dc.DrawStringAnchored(abbr, cx, baseline, 0.5, 0)

	// ---- Layer 4: ECG mid (50% opacity, overlays text per design).
	// Must use color.NRGBA (straight alpha) not color.RGBA (premultiplied):
	// with RGBA, R/G/B > A violates the premultiplied invariant and wraps on compositing.
	drawECG(dc, sc, color.NRGBA{R: sr8, G: sg8, B: sb8, A: 127}, 2.5*sc)

	// ---- Layer 5: ECG top (dark, thin)
	dr8, dg8, db8 := hslToRGB(float64(col.Hue)/360, 0.60, 0.45)
	drawECG(dc, sc, color.RGBA{R: dr8, G: dg8, B: db8, A: 255}, 0.2*sc)

	var buf bytes.Buffer
	if err := png.Encode(&buf, dc.Image()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drawECG draws the ECG polyline scaled from the 64x64 viewBox.
// Path: M 5,32 L 18,32 L 22,22 L 26,40 L 30,32 L 59,32
func drawECG(dc *gg.Context, sc float64, col color.Color, strokeWidth float64) {
	pts := [][2]float64{
		{5, 32}, {18, 32}, {22, 22}, {26, 40}, {30, 32}, {59, 32},
	}
	dc.SetColor(col)
	dc.SetLineWidth(strokeWidth)
	dc.SetLineCapRound()
	dc.SetLineJoinRound()
	dc.MoveTo(pts[0][0]*sc, pts[0][1]*sc)
	for _, p := range pts[1:] {
		dc.LineTo(p[0]*sc, p[1]*sc)
	}
	dc.Stroke()
}

// loadBoldFace returns a bold font.Face at the given point size using the
// embedded Go Bold font — no file system access required.
func loadBoldFace(pts float64) (font.Face, error) {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size: pts,
		DPI:  72,
	})
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t++
	}
	if t > 1 {
		t--
	}
	switch {
	case t < 1.0/6:
		return p + (q-p)*6*t
	case t < 1.0/2:
		return q
	case t < 2.0/3:
		return p + (q-p)*(2.0/3-t)*6
	default:
		return p
	}
}
