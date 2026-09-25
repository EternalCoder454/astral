package ui

import (
	"math"
	"time"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// TypingDots is the three-dot indicator shown while a reply is on its way but
// nothing has arrived yet.
//
// An empty bubble is indistinguishable from a frozen one. The gap before the
// first token is not small either — a large model has to be loaded, then read
// the whole context — so this is the difference between the app looking like
// it is thinking and looking like it has crashed.
type TypingDots struct {
	*gtk.DrawingArea
	start  time.Time
	last   time.Time
	tickID uint
}

const (
	dotCount   = 3
	dotRadius  = 3.5
	dotSpacing = 11.0
	// dotCycle is how long one full wave takes to cross the three dots.
	dotCycle = 1.1
)

// NewTypingDots builds the indicator. It does not animate until Start.
func NewTypingDots() *TypingDots {
	d := &TypingDots{DrawingArea: gtk.NewDrawingArea(), start: time.Now(), last: time.Now()}
	d.SetContentWidth(int(dotSpacing*(dotCount-1) + dotRadius*2 + 2))
	d.SetContentHeight(16)
	d.SetHAlign(gtk.AlignStart)
	d.SetVAlign(gtk.AlignCenter)
	d.SetDrawFunc(d.draw)
	d.SetVisible(false)
	return d
}

// Start shows the dots and begins the animation.
func (d *TypingDots) Start() {
	d.SetVisible(true)
	d.start = time.Now()
	if d.tickID != 0 {
		return
	}
	d.last = time.Now()
	d.tickID = d.AddTickCallback(d.tick)
}

// Stop hides the dots and halts the animation. Stopping matters: this is a
// frame-clock callback, and left running it would repaint every frame for the
// whole time the app sits idle.
func (d *TypingDots) Stop() {
	d.SetVisible(false)
	if d.tickID == 0 {
		return
	}
	d.RemoveTickCallback(d.tickID)
	d.tickID = 0
}

func (d *TypingDots) tick(_ gtk.Widgetter, _ gdk.FrameClocker) bool {
	now := time.Now()
	if now.Sub(d.last) < 40*time.Millisecond { // 25fps is plenty for three dots
		return true
	}
	d.last = now
	d.QueueDraw()
	return true
}

func (d *TypingDots) draw(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) {
	t := time.Since(d.start).Seconds()
	cy := float64(h) / 2
	// Drawn in the text colour at low alpha rather than the accent: this is a
	// placeholder for text, and it should read as text that has not arrived
	// rather than as a control.
	fg := dotColor(d)

	for i := 0; i < dotCount; i++ {
		// Each dot runs the same wave, a third of a cycle behind the last.
		phase := math.Mod(t/dotCycle-float64(i)*0.22, 1)
		if phase < 0 {
			phase++
		}
		// A raised sine: mostly dim, with a brief swell as the wave passes.
		swell := math.Max(0, math.Sin(phase*2*math.Pi))
		alpha := 0.25 + 0.55*swell
		lift := -1.6 * swell // the dot rises very slightly at its peak

		cr.SetSourceRGBA(fg.r, fg.g, fg.b, alpha)
		cr.Arc(dotRadius+1+float64(i)*dotSpacing, cy+lift, dotRadius, 0, 2*math.Pi)
		cr.Fill()
	}
}

type rgb struct{ r, g, b float64 }

// dotColor reads the widget's own foreground colour, so the dots follow the
// light and dark schemes without either being hardcoded here.
//
// Widget.Color rather than StyleContext().Color(): style contexts are
// deprecated and go away in GTK 5.
func dotColor(w gtk.Widgetter) rgb {
	c := gtk.BaseWidget(w).Color()
	if c == nil {
		return rgb{0.6, 0.6, 0.6} // a readable mid-grey on either scheme
	}
	return rgb{float64(c.Red()), float64(c.Green()), float64(c.Blue())}
}
