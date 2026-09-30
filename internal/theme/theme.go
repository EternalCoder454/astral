// Package theme is Astral's colour themes.
//
// A theme is a complete set of colours: the 27 named colours libadwaita draws
// every stock widget with, and the 11 Astral's own stylesheet is written in. It
// also decides whether libadwaita draws its light or its dark widgets, which is
// what Dark is for. Everything else about the look is structure, and lives in
// assets/style.css, which never names a colour of its own. Redefining the names is
// therefore all it takes to re-theme the whole window, and to do it live.
//
// There are nine, and following the desktop is not one of them. Ink is the
// default, a deep blue-violet with a dusk rose accent, and Paper is its light
// scheme. Ember is the warm brown-black and clay Astral used before Ink. Nord,
// Plum and Contrast are dark and Sage, Rose and Solarized are light. Following the
// desktop picks Ink or Paper by what the desktop is set to.
//
// The levels are a ladder, and a ladder is only one if every rung can be seen.
// That is harder in ink than it was in the browns, because a colour this dark
// compresses: the first attempt at Ink put the canvas 1.09:1 above the sidebar
// and the bubbles 1.12:1 above the canvas, which is invisible. It was the
// mistake an earlier Ember had made, with bubbles 1.15:1 above the canvas: the
// transcript came out as one flat wash that was hard to read although the text
// itself was at 12:1 and passed every check. So the ladder is solved rather
// than chosen: every pair was computed, with the palette before it as the
// floor. Ink's body text is at least 8:1 on every surface it can land on, and
// each surface is at least 1.18:1 above the one underneath.
//
// Light mode inverts the bubble relationship instead of copying it. Near white
// there is no luminance headroom above the canvas, so a bubble cannot sit above it
// the way it does in a dark theme, which is why every light messaging app makes the
// incoming bubble slightly darker than the page. Paper does the same and leans
// harder on the border.
//
// TestPalettesAreReadable holds every theme to at least what Ink and Paper
// achieve. It is not a formality. Ember shipped with a white label on its clay
// button at 3.12:1, and white on Ink's rose would have been 3.24:1. A screenshot
// shows neither, because a button reads fine at a glance until you have to read
// the label on one.
//
// The colours are written in both of libadwaita's syntaxes, because it uses both.
// Its stylesheet defines CSS custom properties and named colours, and different
// rules read different ones: the window background comes through @window_bg_color
// while a link is `color: var(--accent-color)`. Overriding only one form
// re-themes about half the window. assets/style.css is written in the
// named-colour syntax, so that form is needed regardless.
package theme

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// Default is the theme a new install starts on, and what an unknown setting
	// falls back to.
	Default = "ink"

	// Follow is the setting's value when no theme has been chosen: Astral tracks
	// the desktop's own light and dark preference and draws Ink or Paper to
	// match. There is deliberately no circle for it in the picker. "Whatever the
	// desktop says" is not a palette, and would be a different kind of thing from
	// the nine beside it.
	Follow = "system"
)

// Theme is one palette.
type Theme struct {
	// ID is what goes in the settings file. It is never translated and never
	// changes, because a renamed one would silently reset everybody's choice.
	ID string
	// Name is what the picker shows.
	Name string
	// Summary is one line about what it is like, in a sentence.
	Summary string

	// Dark says which colour scheme to force. It is not cosmetic: libadwaita's
	// own widgets, the scrollbars and the symbolic icons key off it, so a dark
	// palette drawn under the light scheme would have dark icons on a dark page.
	Dark bool

	// colors is every named colour, keyed by the underscore name. It holds
	// exactly the names in names, which TestEveryThemeDefinesEveryName checks.
	colors palette
}

// palette is a theme's colours by name.
type palette map[string]string

// names is every colour a theme defines, in the order they are written out
// within their group. Astral's own are the ones with the prefix; the rest are
// libadwaita's.
var names = []string{
	// Levels, darkest first in a dark theme.
	"astral_sidebar", "astral_canvas", "astral_surface", "astral_elevated", "astral_border",
	// Text.
	"astral_text", "astral_muted", "astral_faint",
	// libadwaita.
	"window_bg_color", "window_fg_color", "view_bg_color", "view_fg_color",
	"headerbar_bg_color", "headerbar_fg_color", "headerbar_border_color", "headerbar_backdrop_color",
	"sidebar_bg_color", "sidebar_fg_color", "sidebar_backdrop_color", "sidebar_border_color",
	"card_bg_color", "card_fg_color", "dialog_bg_color", "dialog_fg_color",
	"popover_bg_color", "popover_fg_color",
	"accent_bg_color", "accent_fg_color", "accent_color",
	"destructive_bg_color", "destructive_fg_color", "destructive_color",
	"success_color", "warning_color", "error_color",
	// Astral's own, beyond the levels.
	"astral_surface_hover", "astral_composer_bg", "astral_user_bubble",
}

// Color is one of the theme's colours as #rrggbb, or empty for a name it does not
// define.
func (t Theme) Color(name string) string { return t.colors[name] }

// Canvas and Accent are the two halves of the circle in the picker: the chat area
// and the colour of a button. Together they are a fair preview of what choosing
// the theme does, which one colour alone would not be.
func (t Theme) Canvas() string { return t.colors["astral_canvas"] }
func (t Theme) Accent() string { return t.colors["accent_bg_color"] }

// Themes is every theme, in the order the picker shows them.
//
// The default and its light scheme come first, then the palette Astral used
// before Ink, then three dark and three light alternatives interleaved so that
// whichever polarity somebody prefers there is a choice within it.
var Themes = []Theme{
	{
		ID:      "ink",
		Name:    "Ink",
		Summary: "Deep blue-violet with a dusk rose accent.",
		Dark:    true,
		colors: palette{
			// Darkest first. These are solved rather than chosen: see the package comment.
			"astral_sidebar":  "#03050d",
			"astral_canvas":   "#1b1e32",
			"astral_surface":  "#2f3350",
			"astral_elevated": "#393e5d",
			"astral_border":   "#51577d",

			// Secondary text is 10.0:1 on the canvas and captions 7.6:1.
			"astral_text":  "#f5f4fb",
			"astral_muted": "#cac8db",
			"astral_faint": "#b1aec6",

			"window_bg_color": "#1b1e32",
			"window_fg_color": "#f5f4fb",
			"view_bg_color":   "#1b1e32",
			"view_fg_color":   "#f5f4fb",

			"headerbar_bg_color":       "#1b1e32",
			"headerbar_fg_color":       "#f5f4fb",
			"headerbar_border_color":   "#51577d",
			"headerbar_backdrop_color": "#1b1e32",

			"sidebar_bg_color":       "#03050d",
			"sidebar_fg_color":       "#f5f4fb",
			"sidebar_backdrop_color": "#03050d",
			"sidebar_border_color":   "#51577d",

			"card_bg_color":    "#2f3350",
			"card_fg_color":    "#f5f4fb",
			"dialog_bg_color":  "#252943",
			"dialog_fg_color":  "#f5f4fb",
			"popover_bg_color": "#393e5d",
			"popover_fg_color": "#f5f4fb",

			// Dusk rose. The label is ink, not white: white on this rose is 3.24:1, which
			// fails to read on a filled button, and the ink is 5.7:1. accent_color is the
			// accent used as text, lightened because the rose itself only reaches 5.1:1 on
			// the canvas and 3.8:1 on a bubble.
			"accent_bg_color": "#c9787f",
			"accent_fg_color": "#1b1119",
			"accent_color":    "#e3a8ad",

			"destructive_bg_color": "#b34a52",
			"destructive_fg_color": "#ffffff",
			"destructive_color":    "#ef9aa0",
			"success_color":        "#8fc9a5",
			"warning_color":        "#e0b981",
			"error_color":          "#ef9aa0",

			// Dusk rose at 40% over the canvas. The two bubbles have to be told apart at a
			// glance or the transcript reads as one column: this is 1.41:1 against the
			// character's bubble, with body text still at 8.0:1 on it.
			"astral_surface_hover": "#383d5e",
			"astral_composer_bg":   "#393e5d",
			"astral_user_bubble":   "#614251"},
	},
	{
		ID:      "paper",
		Name:    "Paper",
		Summary: "Ink's light scheme: pale lavender with a deeper rose.",
		Dark:    false,
		colors: palette{
			// The bubble sits below the canvas rather than above it: near white there is no
			// headroom above the page, which is why every light messaging app draws the
			// incoming bubble slightly darker than the page. Paper leans harder on the
			// border to make up for it.
			"astral_sidebar":  "#e9e8f2",
			"astral_canvas":   "#faf9fd",
			"astral_surface":  "#eceaf5",
			"astral_elevated": "#ffffff",
			"astral_border":   "#c2bfd4",

			// Secondary text is 8.5:1 on the canvas and captions 6.0:1.
			"astral_text":  "#151728",
			"astral_muted": "#454863",
			"astral_faint": "#5c5f78",

			"window_bg_color": "#faf9fd",
			"window_fg_color": "#151728",
			"view_bg_color":   "#faf9fd",
			"view_fg_color":   "#151728",

			"headerbar_bg_color":       "#faf9fd",
			"headerbar_fg_color":       "#151728",
			"headerbar_border_color":   "#c2bfd4",
			"headerbar_backdrop_color": "#faf9fd",

			"sidebar_bg_color":       "#e9e8f2",
			"sidebar_fg_color":       "#151728",
			"sidebar_backdrop_color": "#e9e8f2",
			"sidebar_border_color":   "#c2bfd4",

			"card_bg_color":    "#ffffff",
			"card_fg_color":    "#151728",
			"dialog_bg_color":  "#faf9fd",
			"dialog_fg_color":  "#151728",
			"popover_bg_color": "#ffffff",
			"popover_fg_color": "#151728",

			// Ink's rose, deepened. The same hue at the dark scheme's lightness puts white
			// text at 3.2:1 on a light page, below the bar for a button label; this is
			// the same colour taken down until it clears it, and it doubles as the accent
			// used for text.
			"accent_bg_color": "#a34a56",
			"accent_fg_color": "#ffffff",
			"accent_color":    "#98404c",

			"destructive_bg_color": "#b03a38",
			"destructive_fg_color": "#ffffff",
			"destructive_color":    "#a3312f",
			"success_color":        "#3f6b4e",
			"warning_color":        "#8a5a1c",
			"error_color":          "#a3312f",

			// A rose tint, 1.34:1 against the character's bubble, with body text at 11.2:1 on it.
			"astral_surface_hover": "#e2e0ee",
			"astral_composer_bg":   "#ffffff",
			"astral_user_bubble":   "#e6c5cb"},
	},
	{
		ID:      "ember",
		Name:    "Ember",
		Summary: "Warm brown-black with a clay accent.",
		Dark:    true,
		colors: palette{
			// Brown-blacks rather than neutral ones. That one degree of warmth is most of
			// what separates this from a generic dark theme.
			"astral_sidebar":  "#10100e",
			"astral_canvas":   "#262420",
			"astral_surface":  "#3b3832",
			"astral_elevated": "#434039",
			"astral_border":   "#575146",

			"astral_text":  "#f7f6f1",
			"astral_muted": "#c8c4b9",
			"astral_faint": "#aea99d",

			"window_bg_color": "#262420",
			"window_fg_color": "#f7f6f1",
			"view_bg_color":   "#262420",
			"view_fg_color":   "#f7f6f1",

			"headerbar_bg_color":       "#262420",
			"headerbar_fg_color":       "#f7f6f1",
			"headerbar_border_color":   "#575146",
			"headerbar_backdrop_color": "#262420",

			"sidebar_bg_color":       "#10100e",
			"sidebar_fg_color":       "#f7f6f1",
			"sidebar_backdrop_color": "#10100e",
			"sidebar_border_color":   "#575146",

			"card_bg_color":    "#3b3832",
			"card_fg_color":    "#f7f6f1",
			"dialog_bg_color":  "#2f2d28",
			"dialog_fg_color":  "#f7f6f1",
			"popover_bg_color": "#434039",
			"popover_fg_color": "#f7f6f1",

			// Clay. The label is a dark brown, not white: white on this clay is 3.12:1,
			// which is what the palette shipped with, and the brown is 5.7:1. accent_color
			// is the accent used as text, lightened because the clay does not carry a
			// reading contrast against a dark canvas.
			"accent_bg_color": "#d97757",
			"accent_fg_color": "#1f1712",
			"accent_color":    "#eda488",

			"destructive_bg_color": "#c0392b",
			"destructive_fg_color": "#ffffff",
			"destructive_color":    "#ef8b80",
			"success_color":        "#a9c488",
			"warning_color":        "#e8b57a",
			"error_color":          "#ef8b80",

			// Clay at 38% over the canvas: 1.39:1 against the character's bubble, with body
			// text still at 7.8:1 on it. It was 22%, which put the two bubbles 1.05:1 apart.
			"astral_surface_hover": "#47443c",
			"astral_composer_bg":   "#434039",
			"astral_user_bubble":   "#6a4435"},
	},
	derive(seed{
		id: "nord", name: "Nord", summary: "Cool blue-grey with a frost accent.", dark: true,
		// The Nord palette: polar night for the greys, snow storm for the text,
		// frost for the accent. Nothing in it is fully saturated, which is easy to
		// read for a long time. Its frost blue carries a dark label: Nord's own
		// deeper blue, #5e81ac, with a white one is 3.5:1.
		//
		// The page is a shade deeper than Nord's own #2e3440, which becomes about
		// the level of a bubble. With that as the canvas, the character's bubble and
		// then the user's have to sit above it in turn, and the user's ended up with
		// its text at 6.0:1, short of the 7:1 every other theme keeps there.
		canvas: "#20252e", text: "#eceff4", accent: "#88c0d0",
		danger: "#bf616a", success: "#a3be8c", warning: "#ebcb8b",
	}),
	derive(seed{
		id: "sage", name: "Sage", summary: "Warm paper and a muted green, easy in daylight.", dark: false,
		// Paper rather than white, which takes the glare off a bright room without
		// going grey, and a green that does not fight the status colours. The text
		// is a green so dark it reads as black, deep enough to keep 7:1 on the
		// user's bubble, which is the darkest thing it lands on.
		canvas: "#f6f5ec", text: "#1f2b21", accent: "#4e7850",
		danger: "#b3392f", success: "#3f7a4a", warning: "#a1661b",
	}),
	derive(seed{
		id: "plum", name: "Plum", summary: "Deep purple with a lilac accent, after Dracula.", dark: true,
		// Dracula's accents on a plum ground rather than its blue-grey, so that
		// it is a different colour from Ink and not only a lighter one. Its lilac
		// is light enough that a white label would be 2.3:1, so the button takes a
		// dark one.
		canvas: "#2a2336", text: "#f6f1fb", accent: "#bd93f9",
		danger: "#ff5555", success: "#50fa7b", warning: "#ffb86c",
	}),
	derive(seed{
		id: "rose", name: "Rose", summary: "Blush and warm white with a raspberry accent.", dark: false,
		// After Rose Pine's Dawn, but with text a good deal darker than its muted
		// violet-grey, which is 6.8:1 on the canvas and so short of the 7:1 the
		// user's bubble needs. The accent is raspberry rather than Paper's dusty
		// red, so that the two light themes with a rose in them are not one theme.
		canvas: "#fbf4ee", text: "#3a2838", accent: "#b0416b",
		danger: "#c22b2b", success: "#2d7a4f", warning: "#9a6412",
	}),
	derive(seed{
		id: "solarized", name: "Solarized", summary: "The classic cream palette with its blue accent.", dark: false,
		// Solarized Light's base3 page. Its blue is designed for text, not for
		// filling buttons, and white on it is 3.6:1, so it is taken darker until
		// the label reads. Body text is base03 rather than the palette's usual
		// base00, which is deliberately soft and too soft for a chat.
		canvas: "#fdf6e3", text: "#002b36", accent: "#268bd2",
		danger: "#dc322f", success: "#859900", warning: "#b58900",
	}),
	derive(seed{
		id: "contrast", name: "Contrast", summary: "Black and white with a yellow accent, for legibility.", dark: true,
		// As legible as colour alone can make it: pure black, pure white, and a
		// yellow accent that reads at 14:1 on the page. The lines are heavy and
		// the secondary text is held nearly as bright as the body. Nothing is
		// darker than black, so its sidebar sits above the page instead of below.
		//
		// It is not libadwaita's high contrast mode, which also thickens borders
		// and outlines. That follows the desktop's accessibility setting and
		// still applies on top of this if it is on.
		canvas: "#000000", text: "#ffffff", accent: "#ffd60a",
		danger: "#ff6b6b", success: "#3ddc84", warning: "#ff9f1a",
		border: 5, muted: 12, faint: 9,
	}),
}

// legacy is the setting's old values. Astral began with a dark and a light scheme
// and a setting that chose between them; they became Ink and Paper.
var legacy = map[string]string{"dark": "ink", "light": "paper"}

// ByID returns a theme by its settings value.
func ByID(id string) (Theme, bool) {
	for _, t := range Themes {
		if t.ID == id {
			return t, true
		}
	}
	return Theme{}, false
}

// Normalise turns whatever a settings file holds into a value the rest of the
// program can use: a theme's ID, or Follow. The two schemes Astral began with
// are carried over to the themes they became, and anything unrecognised, such as
// a name from a newer version, goes to the default rather than to a theme the
// person did not choose.
func Normalise(id string) string {
	if id == Follow {
		return Follow
	}
	if _, ok := ByID(id); ok {
		return id
	}
	if to, ok := legacy[id]; ok {
		return to
	}
	return Default
}

// IsFollowing reports whether a setting means "track the desktop".
func IsFollowing(id string) bool {
	_, ok := ByID(id)
	return !ok
}

// Resolve turns a setting into the theme to actually draw. Following the desktop
// is Ink when the desktop is dark and Paper when it is light.
func Resolve(id string, systemDark bool) Theme {
	if t, ok := ByID(id); ok {
		return t
	}
	if systemDark {
		t, _ := ByID("ink")
		return t
	}
	t, _ := ByID("paper")
	return t
}

// CSS is the theme's colours as a stylesheet, for the provider that is swapped
// when the theme changes.
//
// Sorted, so the output is stable and a change to it is readable in a diff.
func (t Theme) CSS() string {
	var b strings.Builder
	fmt.Fprintf(&b, "/* %s, generated by internal/theme. */\n", t.Name)

	// The custom properties, for libadwaita's own rules that read several colours
	// only through them: a link is `var(--accent-color)`, so a theme that set the
	// named colours alone would leave links on the desktop's accent. Astral's
	// own names have no custom property, so they are not written here.
	sorted := make([]string, 0, len(t.colors))
	for name := range t.colors {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	b.WriteString(":root {\n")
	for _, name := range sorted {
		if !strings.HasPrefix(name, "astral_") {
			fmt.Fprintf(&b, "  --%s: %s;\n", strings.ReplaceAll(name, "_", "-"), t.colors[name])
		}
	}
	b.WriteString("}\n")

	// And the named colours, which are what assets/style.css is written in and
	// what most of libadwaita's own rules still use.
	for _, name := range sorted {
		fmt.Fprintf(&b, "@define-color %s %s;\n", name, t.colors[name])
	}
	return b.String()
}

// SwatchClass is the CSS class carrying one theme's colours in the picker.
func SwatchClass(id string) string { return "theme-swatch-" + id }

// SwatchCSS is the styling for every theme's circle in the picker.
//
// It is generated from the table rather than written in assets/style.css so that
// adding a theme cannot leave its circle the wrong colour, or absent. Each is a
// disc split on the diagonal: the canvas on one side, the accent on the other.
func SwatchCSS() string {
	var b strings.Builder
	b.WriteString("/* The picker's circles, generated by internal/theme. See SwatchCSS. */\n")
	for _, t := range Themes {
		// Both classes in the selector on purpose. A single class ties with
		// Adwaita's own `.toggle` rules and loses to anything more specific, and
		// the background-image is the whole point of the widget.
		fmt.Fprintf(&b, ".theme-swatch.%s {\n", SwatchClass(t.ID))
		// A hard stop at the midpoint rather than a blend: the two colours are
		// two facts about the theme, and a gradient between them would invent a
		// third that is not in it.
		fmt.Fprintf(&b, "  background-image: linear-gradient(135deg, %s 0%%, %s 50%%, %s 50%%, %s 100%%);\n",
			t.Canvas(), t.Canvas(), t.Accent(), t.Accent())
		b.WriteString("}\n")
	}
	return b.String()
}
