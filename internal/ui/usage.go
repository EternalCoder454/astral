package ui

import (
	"fmt"
	"html"
	"math"
	"strconv"

	"github.com/diamondburned/gotk4/pkg/cairo"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/gpu"
)

// How full the model's memory is, beside the send button, the way Claude shows
// its context window: a ring that fills as the conversation grows, and a
// popover with what fills it, how much room is left before the oldest turns
// fold into the recap, a way to fold them now, and the video memory the model
// lives in, which on a machine running its own models is the limit that
// matters.
//
// Measured when something changes it (a chat opening, a reply landing, a
// recap written) and when the popover opens, not on every keystroke:
// measuring builds the next request.
//
// The panel is built once, with the chat view, and filled in each time it
// opens. Its draw funcs and handlers hold the view, which lives as long as the
// window does; built anew each time it opened, they would be cycles gotk4
// cannot collect, one set per look at it.

// usagePalette colours the parts of the bar and their legend, in order. Mid
// tones that read on the light themes and the dark ones alike.
var usagePalette = []rgb{
	{0.40, 0.58, 0.93}, // blue
	{0.91, 0.66, 0.30}, // amber
	{0.47, 0.73, 0.42}, // green
	{0.71, 0.54, 0.90}, // purple
	{0.90, 0.42, 0.47}, // red
	{0.35, 0.74, 0.80}, // teal
}

func (p rgb) hex() string {
	return fmt.Sprintf("#%02x%02x%02x", int(p.r*255), int(p.g*255), int(p.b*255))
}

// usageSeg is one coloured part of the bar.
type usageSeg struct {
	name   string
	tokens int
	col    rgb
}

// usageView is the ring and its panel.
type usageView struct {
	btn  *gtk.MenuButton
	ring *gtk.DrawingArea
	frac float64

	figure *gtk.Label
	bar    *gtk.DrawingArea
	segs   []usageSeg
	window int
	reply  int
	legend *gtk.Box

	foldRow  *gtk.Box
	foldNote *gtk.Label
	foldBtn  *gtk.Button

	vramFigure *gtk.Label
	vramBar    *gtk.LevelBar
	model      *gtk.Label

	// pending says a measure is already queued; reads counts video memory
	// reads, so a slow one cannot overwrite a later one.
	pending bool
	reads   int
}

// buildUsageButton makes the ring and its popover.
func (c *ChatView) buildUsageButton() *gtk.MenuButton {
	v := &usageView{}
	c.usage = v
	v.ring = gtk.NewDrawingArea()
	v.ring.SetContentWidth(18)
	v.ring.SetContentHeight(18)
	v.ring.SetDrawFunc(func(area *gtk.DrawingArea, cr *cairo.Context, w, h int) {
		drawRing(area, cr, w, h, v.frac)
	})
	v.btn = gtk.NewMenuButton()
	v.btn.AddCSSClass("composer-model")
	v.btn.AddCSSClass("usage-ring")
	v.btn.SetChild(v.ring)
	v.btn.SetVisible(false)

	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.AddCSSClass("usage-panel")
	box.SetSizeRequest(300, -1)

	head, figure := usageHeader("Context Window")
	v.figure = figure
	box.Append(head)

	v.bar = gtk.NewDrawingArea()
	v.bar.SetContentHeight(8)
	v.bar.SetHExpand(true)
	v.bar.SetDrawFunc(func(area *gtk.DrawingArea, cr *cairo.Context, w, h int) {
		drawUsageBar(area, cr, w, h, v.segs, v.window, v.reply)
	})
	box.Append(v.bar)

	v.legend = gtk.NewBox(gtk.OrientationVertical, 2)
	box.Append(v.legend)

	v.foldRow = gtk.NewBox(gtk.OrientationVertical, 8)
	v.foldRow.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	v.foldNote = gtk.NewLabel("")
	v.foldNote.SetXAlign(0)
	v.foldNote.SetWrap(true)
	v.foldNote.SetHExpand(true)
	v.foldNote.AddCSSClass("usage-note")
	row.Append(v.foldNote)
	v.foldBtn = gtk.NewButtonWithLabel("Compact Now")
	v.foldBtn.AddCSSClass("flat")
	v.foldBtn.SetVAlign(gtk.AlignCenter)
	v.foldBtn.SetTooltipText("Fold all but the newest messages into the recap now")
	v.foldBtn.ConnectClicked(func() {
		switch {
		case c.busy || c.bg.running:
			v.foldBtn.SetLabel("Busy, Try Again Soon")
			return
		case c.CompactNow():
			v.foldBtn.SetLabel("Compacting…")
		default:
			v.foldBtn.SetLabel("Nothing to Compact")
		}
		v.foldBtn.SetSensitive(false)
	})
	row.Append(v.foldBtn)
	v.foldRow.Append(row)
	box.Append(v.foldRow)

	// The limit a local model runs into: the card it is loaded on.
	box.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	vhead, vfig := usageHeader("Video Memory")
	v.vramFigure = vfig
	box.Append(vhead)
	v.vramBar = gtk.NewLevelBar()
	v.vramBar.SetMinValue(0)
	v.vramBar.SetMaxValue(1)
	box.Append(v.vramBar)
	v.model = gtk.NewLabel("")
	v.model.SetXAlign(0)
	v.model.SetEllipsize(pango.EllipsizeEnd)
	v.model.AddCSSClass("usage-note")
	box.Append(v.model)

	pop := gtk.NewPopover()
	pop.AddCSSClass("usage-popover")
	// Upward: the ring is at the bottom of the window, and a popover opening
	// down from it hangs off the window's edge.
	pop.SetPosition(gtk.PosTop)
	pop.SetChild(box)
	v.btn.SetPopover(pop)
	pop.ConnectShow(c.fillUsagePanel)
	return v.btn
}

// drawRing draws the track and the part of it that is used, in the foreground
// colour, turning amber past three quarters and red past nine tenths.
func drawRing(area *gtk.DrawingArea, cr *cairo.Context, w, h int, frac float64) {
	fg := dotColor(area)
	cx, cy := float64(w)/2, float64(h)/2
	r := math.Min(cx, cy) - 2
	cr.SetLineWidth(2.4)
	cr.SetSourceRGBA(fg.r, fg.g, fg.b, 0.38)
	cr.Arc(cx, cy, r, 0, 2*math.Pi)
	cr.Stroke()
	if frac <= 0 {
		return
	}
	col := fg
	switch {
	case frac >= 0.9:
		col = usagePalette[4]
	case frac >= 0.75:
		col = usagePalette[1]
	}
	cr.SetSourceRGBA(col.r, col.g, col.b, 0.95)
	start := -math.Pi / 2
	cr.Arc(cx, cy, r, start, start+2*math.Pi*math.Min(frac, 1))
	cr.Stroke()
}

// drawUsageBar draws the parts in their colours, then the room kept for the
// reply, on a faint track the width of the whole window.
func drawUsageBar(area *gtk.DrawingArea, cr *cairo.Context, w, h int, segs []usageSeg, window, reply int) {
	fg := dotColor(area)
	cr.SetSourceRGBA(fg.r, fg.g, fg.b, 0.12)
	cr.Rectangle(0, 0, float64(w), float64(h))
	cr.Fill()
	total := float64(max(window, 1))
	x := 0.0
	for _, s := range segs {
		sw := math.Min(float64(w)*float64(s.tokens)/total, float64(w)-x)
		cr.SetSourceRGBA(s.col.r, s.col.g, s.col.b, 1)
		cr.Rectangle(x, 0, sw, float64(h))
		cr.Fill()
		x += sw
	}
	cr.SetSourceRGBA(fg.r, fg.g, fg.b, 0.35)
	cr.Rectangle(x, 0, math.Min(float64(w)*float64(reply)/total, float64(w)-x), float64(h))
	cr.Fill()
}

// scheduleUsage measures the chat a moment from now, once however many
// changes arrive together, and not while a reply is being written: measuring
// builds the next request, and sending already builds one.
func (c *ChatView) scheduleUsage() {
	v := c.usage
	if v == nil || v.pending {
		return
	}
	v.pending = true
	coreglib.TimeoutAdd(300, func() bool {
		v.pending = false
		if !c.busy {
			c.refreshUsage()
		}
		return false
	})
}

// usageCompacted refills the popover when a recap lands while it is open, so
// Compact Now does not stay on "Compacting…".
func (c *ChatView) usageCompacted() {
	if v := c.usage; v != nil && v.btn.Popover() != nil && v.btn.Popover().Visible() {
		c.fillUsagePanel()
	}
}

// refreshUsage measures the chat and redraws the ring. Hidden for a chat with
// nothing in it yet, where there is nothing to measure.
func (c *ChatView) refreshUsage() {
	v := c.usage
	if v == nil {
		return
	}
	if len(c.history()) == 0 {
		v.btn.SetVisible(false)
		return
	}
	u := c.Usage()
	v.frac = float64(u.Used+u.Reply) / float64(max(u.Window, 1))
	v.btn.SetVisible(true)
	v.btn.SetTooltipText(fmt.Sprintf("Context: %d%% used", int(v.frac*100+0.5)))
	v.ring.QueueDraw()
}

// fillUsagePanel measures the chat and fills the panel in, as it opens.
func (c *ChatView) fillUsagePanel() {
	v := c.usage
	u := c.Usage()
	total := u.Used + u.Reply
	v.frac = float64(total) / float64(max(u.Window, 1))
	v.ring.QueueDraw()
	v.figure.SetText(fmt.Sprintf("%s / %s (%d%%)", tokenCount(total), tokenCount(u.Window), int(v.frac*100+0.5)))

	v.segs = v.segs[:0]
	for i, p := range u.Parts {
		if p.Tokens > 0 {
			v.segs = append(v.segs, usageSeg{p.Name, p.Tokens, usagePalette[i%len(usagePalette)]})
		}
	}
	v.window, v.reply = u.Window, u.Reply
	v.bar.QueueDraw()

	for ch := v.legend.FirstChild(); ch != nil; ch = v.legend.FirstChild() {
		v.legend.Remove(ch)
	}
	for _, s := range v.segs {
		v.legend.Append(legendItem(s.col.hex(), s.name, s.tokens))
	}
	v.legend.Append(legendItem("", "Kept for the Reply", u.Reply))

	v.foldRow.SetVisible(u.FoldsAt > 0)
	if left := u.FoldsAt - u.Conversation; left > 0 {
		v.foldNote.SetText(tokenCount(left) + " until the oldest turns fold into the recap")
	} else {
		v.foldNote.SetText("The oldest turns fold into the recap after the next reply")
	}
	v.foldBtn.SetLabel("Compact Now")
	v.foldBtn.SetSensitive(!c.busy && !c.bg.running)

	v.model.SetText(c.activeModel())
	v.vramFigure.SetText("…")
	v.reads++
	read := v.reads
	// Read off the UI thread: NVIDIA's is a program that can take its time.
	go func() {
		m, ok := gpu.Read()
		coreglib.IdleAdd(func() bool {
			if read != v.reads {
				return false // a later look is reading it already
			}
			if !ok || m.Total == 0 {
				v.vramFigure.SetText("not readable here")
				v.vramBar.SetVisible(false)
				return false
			}
			f := float64(m.Used) / float64(m.Total)
			v.vramFigure.SetText(fmt.Sprintf("%.1f / %.1f GB (%d%%)", gib(m.Used), gib(m.Total), int(f*100+0.5)))
			v.vramBar.SetVisible(true)
			v.vramBar.SetValue(f)
			return false
		})
	}()
}

// usageHeader is a heading on the left and a figure on the right, and the
// figure, to be filled in.
func usageHeader(title string) (*gtk.Box, *gtk.Label) {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	t := gtk.NewLabel(title)
	t.SetXAlign(0)
	t.SetHExpand(true)
	t.AddCSSClass("usage-title")
	row.Append(t)
	f := gtk.NewLabel("")
	f.SetXAlign(1)
	f.AddCSSClass("usage-figure")
	row.Append(f)
	return row, f
}

// legendItem is a coloured dot, a name and a count. An empty colour is a faint
// foreground one, for the part kept for the reply.
func legendItem(color, name string, tokens int) *gtk.Label {
	dot := `<span alpha="45%">●</span>`
	if color != "" {
		dot = `<span foreground="` + color + `">●</span>`
	}
	l := gtk.NewLabel("")
	l.SetMarkup(dot + " " + html.EscapeString(name) + ` <span alpha="70%">` + tokenCount(tokens) + `</span>`)
	l.SetXAlign(0)
	l.AddCSSClass("usage-note")
	return l
}

// tokenCount writes a token count the way Claude does: 820, 12.4k.
func tokenCount(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
}

func gib(b uint64) float64 { return float64(b) / (1 << 30) }
