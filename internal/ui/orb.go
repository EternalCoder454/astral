package ui

import (
	"math"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Orb is Astral's mark: a Cairo-drawn sphere, used as the logo on the welcome
// screen.
//
// It used to animate. That machinery — a frame-clock tick easing between a
// resting and a working state, with fewer halo rings and no ripples at rest —
// was written for a thinking indicator, and then the job went to TypingDots
// instead, which sits in the bubble where the reply is actually going to
// appear. Nothing ever called the animated constructor, so the tick callback,
// the easing and the two levels of detail are gone and the drawing is a single
// deterministic figure: same logo, every launch.
type Orb struct {
	*gtk.DrawingArea
}

// NewOrb returns an orb of the given pixel size.
func NewOrb(size int) *Orb {
	o := &Orb{DrawingArea: gtk.NewDrawingArea()}
	o.SetContentWidth(size)
	o.SetContentHeight(size)
	o.SetHAlign(gtk.AlignCenter)
	o.SetVAlign(gtk.AlignCenter)
	o.SetDrawFunc(o.draw)
	return o
}

// Clay, the brand accent, and a warm highlight for the core. Hardcoded rather
// than read from the theme: the orb is the one element that should look the
// same in both schemes, because it is the logo.
const (
	orbR, orbG, orbB = 0.851, 0.467, 0.341 // #d97757
	litR, litG, litB = 0.961, 0.820, 0.745 // warm highlight
)

func (o *Orb) draw(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) {
	s := math.Min(float64(w), float64(h))
	if s <= 0 {
		return
	}
	cx, cy := float64(w)/2, float64(h)/2
	R := s * 0.34     // outer ring radius
	coreR := s * 0.16 // nucleus radius

	// Halo: concentric translucent fills, fading outward.
	const haloN = 5
	for i := haloN; i >= 1; i-- {
		f := float64(i) / haloN
		cr.SetSourceRGBA(orbR, orbG, orbB, 0.05*(1-f*0.85))
		cr.Arc(cx, cy, coreR+(R*1.15-coreR)*f, 0, 2*math.Pi)
		cr.Fill()
	}

	// Outer ring.
	cr.SetSourceRGBA(orbR, orbG, orbB, 0.50)
	cr.SetLineWidth(s * 0.022)
	cr.Arc(cx, cy, R, 0, 2*math.Pi)
	cr.Stroke()

	// Nucleus: a blob rather than a circle, so the mark has a little life in
	// it. Three harmonics, at a fixed phase — one circle would read as a dot.
	const pts = 32
	for i := 0; i <= pts; i++ {
		ang := 2 * math.Pi * float64(i) / pts
		wob := 0.55*math.Sin(3*ang) + 0.30*math.Sin(5*ang) + 0.15*math.Sin(2*ang)
		rr := coreR * (1 + 0.10*wob*0.5)
		x, y := cx+rr*math.Cos(ang), cy+rr*math.Sin(ang)
		if i == 0 {
			cr.MoveTo(x, y)
		} else {
			cr.LineTo(x, y)
		}
	}
	cr.ClosePath()
	cr.SetSourceRGBA(litR, litG, litB, 0.45)
	cr.Fill()

	// A bright core, offset a touch so the sphere reads as lit from one side.
	hx, hy := cx+coreR*0.12, cy-coreR*0.10
	for i := 3; i >= 1; i-- {
		f := float64(i) / 3
		cr.SetSourceRGBA(1, 1, 1, 0.12*(1-0.55*f))
		cr.Arc(hx, hy, coreR*(0.35+0.5*f), 0, 2*math.Pi)
		cr.Fill()
	}
	cr.SetSourceRGBA(1, 1, 1, 0.92)
	cr.Arc(hx, hy, coreR*0.32, 0, 2*math.Pi)
	cr.Fill()
}
