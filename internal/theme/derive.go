package theme

// A palette worked out from three colours.
//
// Ink, Paper and Ember are written out by hand, because they were chosen by eye
// and measured afterwards. Everything else is solved: a theme is its canvas, its
// text and its accent, and the rest follows from what has to be true of it. That
// is not economy. Choosing a ladder by eye is how the first Ink put its canvas
// 1.09:1 above the sidebar and its bubbles 1.12:1 above the canvas, which is to
// say invisible, and how the warm scheme before it had bubbles at 1.15:1. A
// level is only a level if it can be seen, so each is the smallest step that can.

// seed is what a derived palette is chosen by.
type seed struct {
	id, name, summary string
	dark              bool

	canvas string // the chat area: the colour everything else is measured from
	text   string // body text
	accent string // the colour of a button, before it is adjusted to carry a label

	// The status hues, before they are adjusted to be read as text. Empty takes
	// the default for the theme's polarity.
	danger, success, warning string

	// The solved targets, where a theme needs something other than the default:
	// Contrast wants harder lines and quieter text held further from the
	// background than an ordinary theme does. Zero takes the default.
	border, muted, faint float64
}

// The contrast each level of a ladder is solved for, taken from what Ink and Paper
// measure. Ink's steps are 1.24, 1.34 and 1.18 (sidebar, surface, and the
// composer above the surface); Paper's are 1.16 and 1.14. The light targets are
// lower because a light page has no headroom above it: on near white the level
// cannot go up, only down, and a step as big as a dark theme's would be a
// different colour altogether.
type ladder struct {
	sidebar, surface, elevated, hover, border float64
}

var (
	darkLadder  = ladder{sidebar: 1.23, surface: 1.33, elevated: 1.18, hover: 1.16, border: 2.3}
	lightLadder = ladder{sidebar: 1.18, surface: 1.16, hover: 1.09, border: 1.7}
)

const (
	// bubbleStep is how far the user's bubble sits from the character's. Ink is
	// 1.41 and Paper 1.34: enough that the two sides of a conversation are told
	// apart at a glance instead of by which edge they hug.
	bubbleStep = 1.36

	// mutedRatio and faintRatio are what secondary text and captions keep on the
	// backgrounds they sit on (Ink: 7.5 and 5.7 on its surface; Paper 7.3 and 5.1).
	mutedRatio = 7.0
	faintRatio = 5.2

	// labelRatio is for the text on a filled button, and accentTextRatio and
	// statusRatio for a colour used as text: the accent and the status colours.
	// All are comfortably over WCAG's 4.5, which is the floor the tests hold.
	labelRatio      = 4.8
	accentTextRatio = 5.0
	statusRatio     = 4.8
)

// The status hues a theme gets when it does not name its own.
var (
	darkStatus  = [3]string{"#ef9aa0", "#8fc9a5", "#e0b981"} // danger, success, warning
	lightStatus = [3]string{"#b03a38", "#3f6b4e", "#8a5a1c"}
)

// derive solves the whole palette from a seed.
func derive(s seed) Theme {
	black, white := rgb{}, rgb{255, 255, 255}
	canvas, text := mustHex(s.canvas), mustHex(s.text)

	lad := lightLadder
	status := lightStatus
	if s.dark {
		lad, status = darkLadder, darkStatus
	}
	if s.border != 0 {
		lad.border = s.border
	}
	pick := func(set, fallback string) rgb {
		if set == "" {
			set = fallback
		}
		return mustHex(set)
	}

	// The levels. A dark theme steps up from the canvas towards its text, so the
	// tint of the text is what colours its greys; the sidebar is the exception
	// and goes down towards black, as the floor under everything. A light theme
	// steps down from the canvas for both, and the cards and the composer are
	// white, above it.
	var sidebar, surface, elevated, card, dialog rgb
	if s.dark {
		// Nothing is darker than black, so a canvas that is already at the floor
		// (Contrast's) puts its sidebar above it instead.
		if contrast(canvas, black) >= lad.sidebar {
			sidebar = reach(canvas, black, want(lad.sidebar, canvas))
		} else {
			sidebar = reach(canvas, text, want(lad.sidebar, canvas))
		}
		surface = reach(canvas, text, want(lad.surface, canvas))
		elevated = reach(surface, text, want(lad.elevated, surface))
		card = surface
		// Between the canvas and the surface, as Ink's is.
		dialog = mix(canvas, surface, 0.5)
	} else {
		sidebar = reach(canvas, text, want(lad.sidebar, canvas))
		surface = reach(canvas, text, want(lad.surface, canvas))
		elevated = white
		card = elevated
		dialog = canvas
	}
	border := reach(canvas, text, want(lad.border, canvas))
	hover := reach(surface, text, want(lad.hover, surface))
	grounds := []rgb{sidebar, canvas, surface, elevated}

	// The accent as a fill. A light theme's button takes a white label and is
	// deepened until the label reads. A dark theme's takes whichever of white and
	// a dark ink reads better on it, which is the dark one for any accent light
	// enough to be seen against the canvas, and is deepened or lightened only if
	// even that falls short.
	ink := mix(canvas, black, 0.6)
	if !s.dark {
		ink = text
	}
	accent := mustHex(s.accent)
	label := white
	switch {
	case !s.dark:
		accent = reach(accent, black, want(labelRatio, white))
	default:
		cw, ck := contrast(accent, white), contrast(accent, ink)
		if ck >= cw {
			label = ink
			accent = reach(accent, white, want(labelRatio, ink))
		} else {
			accent = reach(accent, black, want(labelRatio, white))
		}
	}
	// The accent as text, taken towards the body text until it reads on every
	// level, which for a light accent on a dark theme is not at all.
	accentText := reach(accent, text, want(accentTextRatio, grounds...))

	destructive := reach(pick(s.danger, status[0]), black, want(labelRatio, white))
	readable := func(c rgb) rgb { return reach(c, text, want(statusRatio, grounds...)) }
	danger := readable(pick(s.danger, status[0]))

	// The user's bubble is the canvas tinted with the accent, by as little as
	// tells it from the character's bubble, which is the surface. The scan skips
	// the near side of the surface: a bubble that is on the canvas's side of it
	// can be no further from the surface than the canvas is.
	bubble := canvas
	for i := 1; i <= 700; i++ {
		c := mix(canvas, accent, float64(i)/1000)
		farSide := luminance(c) > luminance(surface)
		if !s.dark {
			farSide = luminance(c) < luminance(surface)
		}
		if farSide && contrast(c, surface) >= bubbleStep {
			bubble = c
			break
		}
	}

	mutedR, faintR := mutedRatio, faintRatio
	if s.muted != 0 {
		mutedR = s.muted
	}
	if s.faint != 0 {
		faintR = s.faint
	}
	// Secondary text is the body text faded towards the canvas as far as it can
	// go and still meet its ratio on the three levels it lives on, and on the
	// user's bubble at a lower one: a timestamp inside it is read less than a
	// caption on the canvas, and holding it to the same ratio would make every
	// theme's secondary text as loud as its body.
	muted := fade(text, canvas, append(want(mutedR, sidebar, canvas, surface), want(5, bubble)...))
	faint := fade(text, canvas, append(want(faintR, sidebar, canvas, surface), want(4.5, bubble)...))

	c := palette{
		"astral_sidebar":  sidebar.hex(),
		"astral_canvas":   canvas.hex(),
		"astral_surface":  surface.hex(),
		"astral_elevated": elevated.hex(),
		"astral_border":   border.hex(),

		"astral_text":  text.hex(),
		"astral_muted": muted.hex(),
		"astral_faint": faint.hex(),

		"window_bg_color": canvas.hex(),
		"window_fg_color": text.hex(),
		"view_bg_color":   canvas.hex(),
		"view_fg_color":   text.hex(),

		"headerbar_bg_color":       canvas.hex(),
		"headerbar_fg_color":       text.hex(),
		"headerbar_border_color":   border.hex(),
		"headerbar_backdrop_color": canvas.hex(),

		"sidebar_bg_color":       sidebar.hex(),
		"sidebar_fg_color":       text.hex(),
		"sidebar_backdrop_color": sidebar.hex(),
		"sidebar_border_color":   border.hex(),

		"card_bg_color":    card.hex(),
		"card_fg_color":    text.hex(),
		"dialog_bg_color":  dialog.hex(),
		"dialog_fg_color":  text.hex(),
		"popover_bg_color": elevated.hex(),
		"popover_fg_color": text.hex(),

		"accent_bg_color": accent.hex(),
		"accent_fg_color": label.hex(),
		"accent_color":    accentText.hex(),

		"destructive_bg_color": destructive.hex(),
		"destructive_fg_color": white.hex(),
		"destructive_color":    danger.hex(),
		"success_color":        readable(pick(s.success, status[1])).hex(),
		"warning_color":        readable(pick(s.warning, status[2])).hex(),
		"error_color":          danger.hex(),

		"astral_surface_hover": hover.hex(),
		"astral_composer_bg":   elevated.hex(),
		"astral_user_bubble":   bubble.hex(),
	}
	return Theme{ID: s.id, Name: s.name, Summary: s.summary, Dark: s.dark, colors: c}
}
