package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The arithmetic the palettes are solved with. A colour is three channels on a
// 0 to 255 scale, held as floats so a blend is not rounded to a whole channel
// until it is written out as a hex string.

type rgb struct{ r, g, b float64 }

func parseHex(s string) (rgb, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return rgb{}, fmt.Errorf("not a #rrggbb colour: %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return rgb{}, err
	}
	return rgb{float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff)}, nil
}

// mustHex is parseHex for the palette table, where a malformed colour is a typo
// in the source rather than something to recover from.
func mustHex(s string) rgb {
	c, err := parseHex(s)
	if err != nil {
		panic("theme: " + err.Error())
	}
	return c
}

func (c rgb) hex() string {
	clamp := func(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(255, v)))) }
	return fmt.Sprintf("#%02x%02x%02x", clamp(c.r), clamp(c.g), clamp(c.b))
}

// mix is a blend of a towards b by t, in sRGB, which is how a colour drawn at
// partial opacity over another blends on screen.
func mix(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

// luminance is WCAG 2.1's relative luminance.
func luminance(c rgb) float64 {
	ch := func(v float64) float64 {
		v /= 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.r) + 0.7152*ch(c.g) + 0.0722*ch(c.b)
}

// contrast is WCAG 2.1's ratio: (lighter + 0.05) / (darker + 0.05).
func contrast(a, b rgb) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// contrastHex is contrast for two #rrggbb strings, or 0 when either is not one,
// which no threshold accepts.
func contrastHex(a, b string) float64 {
	ca, errA := parseHex(a)
	cb, errB := parseHex(b)
	if errA != nil || errB != nil {
		return 0
	}
	return contrast(ca, cb)
}

// steps is how finely a blend is searched. Contrast is not linear in the blend,
// and not always monotonic either, so it is scanned rather than solved for.
const steps = 1000

// need is one legibility requirement: a colour has to reach `ratio` contrast
// against `on`.
type need struct {
	ratio float64
	on    rgb
}

// want is the same ratio required against each of several backgrounds.
func want(ratio float64, on ...rgb) []need {
	out := make([]need, len(on))
	for i, bg := range on {
		out[i] = need{ratio, bg}
	}
	return out
}

func met(c rgb, needs []need) bool {
	for _, n := range needs {
		if contrast(c, n.on) < n.ratio {
			return false
		}
	}
	return true
}

// reach is the colour on the line from `from` towards `to` that is nearest
// `from` and still meets every need. It is what a level of a ladder is: the
// smallest step away from the level below that can actually be seen.
//
// When nothing on the line gets there it returns `to` itself, and the palette
// tests, which check every pair, are what report it.
func reach(from, to rgb, needs []need) rgb {
	for i := 0; i <= steps; i++ {
		c := mix(from, to, float64(i)/steps)
		if met(c, needs) {
			return c
		}
	}
	return to
}

// fade is the reverse: the colour on the line from `from` towards `to` that is
// furthest along it while still meeting every need. Muted and faint text are the
// body text taken as far towards the background as it can go and stay legible,
// which makes them a choice of how quiet, not of what hue.
func fade(from, to rgb, needs []need) rgb {
	best := from
	for i := 0; i <= steps; i++ {
		c := mix(from, to, float64(i)/steps)
		if !met(c, needs) {
			break
		}
		best = c
	}
	return best
}
