package theme

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var hexColour = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// TestEveryThemeDefinesEveryName: a colour a theme forgets does not fail, it
// draws as transparent, in that theme only. So each defines all 38, and nothing
// that is not one of them, which would be a misspelt name that styles nothing.
func TestEveryThemeDefinesEveryName(t *testing.T) {
	if len(names) != 38 {
		t.Fatalf("the list of names has %d entries, want 38", len(names))
	}
	known := map[string]bool{}
	for _, n := range names {
		if known[n] {
			t.Errorf("%s is listed twice", n)
		}
		known[n] = true
	}
	for _, th := range Themes {
		for _, n := range names {
			c, ok := th.colors[n]
			if !ok {
				t.Errorf("%s does not define %s", th.ID, n)
				continue
			}
			// Lower-case six-digit hex, because these are pasted straight into CSS
			// and one that does not parse takes the rest of the block with it.
			if !hexColour.MatchString(c) {
				t.Errorf("%s: %s is %q, want lower-case #rrggbb", th.ID, n, c)
			}
		}
		for n := range th.colors {
			if !known[n] {
				t.Errorf("%s defines %s, which is not a colour name", th.ID, n)
			}
		}
	}
}

// TestThemesAreComplete: a theme missing a piece does not fail, it draws wrong. A
// missing name is a blank label under a circle, and a summary is prose, so it is
// held to the writing rules: a sentence, in sentence case.
func TestThemesAreComplete(t *testing.T) {
	want := []string{"ink", "paper", "ember", "nord", "sage", "plum", "rose", "solarized", "contrast"}
	if len(Themes) != len(want) {
		t.Fatalf("got %d themes, want %d", len(Themes), len(want))
	}
	seenID, seenName := map[string]bool{}, map[string]bool{}
	for i, th := range Themes {
		if th.ID != want[i] {
			t.Errorf("theme %d is %q, want %q", i, th.ID, want[i])
		}
		if th.Name == "" || th.Summary == "" {
			t.Errorf("%s is missing a name or a summary", th.ID)
		}
		if seenID[th.ID] || seenName[th.Name] {
			t.Errorf("%s shares an id or a name with another theme, so the settings file could not tell them apart", th.ID)
		}
		seenID[th.ID], seenName[th.Name] = true, true
		if s := th.Summary; s[0] < 'A' || s[0] > 'Z' || !strings.HasSuffix(s, ".") {
			t.Errorf("%s: the summary %q should be a sentence, capitalised and ending in a full stop", th.ID, s)
		}
		if strings.ContainsAny(th.Summary, "\u2013\u2014") || strings.Contains(th.Summary, " - ") {
			t.Errorf("%s: the summary %q has a dash used as punctuation", th.ID, th.Summary)
		}
	}
	if Default != Themes[0].ID {
		t.Errorf("the default is %q but the first theme is %q", Default, Themes[0].ID)
	}
}

// TestThereIsChoiceInBothPolarities: a person who prefers a light window needs
// somewhere to go from Paper, and one who prefers a dark one somewhere from Ink.
func TestThereIsChoiceInBothPolarities(t *testing.T) {
	var light, dark int
	for _, th := range Themes {
		if th.Dark {
			dark++
		} else {
			light++
		}
	}
	if light < 3 || dark < 3 {
		t.Errorf("%d light themes and %d dark, want at least three of each", light, dark)
	}
}

// TestEachThemeLooksDifferent: two themes with the same page and the same accent
// are one theme twice, which is what a circle in the picker would show.
func TestEachThemeLooksDifferent(t *testing.T) {
	seen := map[string]string{}
	for _, th := range Themes {
		key := th.Canvas() + "/" + th.Accent()
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s have the same canvas and accent (%s)", th.ID, other, key)
		}
		seen[key] = th.ID
	}
}

// TestInkAndPaperKeepTheirColours pins the two schemes Astral began with. They
// are the default and what following the desktop draws, and a person who never
// touched the setting should not see either move.
func TestInkAndPaperKeepTheirColours(t *testing.T) {
	for id, want := range map[string]map[string]string{
		"ink": {
			"astral_sidebar": "#03050d", "astral_canvas": "#1b1e32", "astral_surface": "#2f3350",
			"astral_text": "#f5f4fb", "accent_bg_color": "#c9787f", "astral_user_bubble": "#614251",
		},
		"paper": {
			"astral_sidebar": "#e9e8f2", "astral_canvas": "#faf9fd", "astral_surface": "#eceaf5",
			"astral_text": "#151728", "accent_bg_color": "#a34a56", "astral_user_bubble": "#e6c5cb",
		},
	} {
		th, ok := ByID(id)
		if !ok {
			t.Fatalf("%s is missing", id)
		}
		for name, c := range want {
			if got := th.Color(name); got != c {
				t.Errorf("%s: %s is %s, want %s", id, name, got, c)
			}
		}
	}
	if ink, _ := ByID("ink"); !ink.Dark {
		t.Error("Ink is not dark")
	}
	if paper, _ := ByID("paper"); paper.Dark {
		t.Error("Paper is dark")
	}
}

// TestDarkFlagMatchesPalette: the flag is not decoration. It forces libadwaita's
// colour scheme, so a theme with a dark page marked light would have dark icons
// and dark scrollbars on it.
func TestDarkFlagMatchesPalette(t *testing.T) {
	for _, th := range Themes {
		lum := relativeLuminance(t, th.Canvas())
		if th.Dark && lum > 0.2 {
			t.Errorf("%s is marked dark but its canvas %s is light (%.2f)", th.ID, th.Canvas(), lum)
		}
		if !th.Dark && lum < 0.5 {
			t.Errorf("%s is marked light but its canvas %s is dark (%.2f)", th.ID, th.Canvas(), lum)
		}
	}
}

// TestPalettesAreReadable holds every theme to at least what Ink and Paper
// achieve, which were measured first. Each threshold is what those two clear,
// rounded down, with one exception noted below.
//
// The case that matters most is the one a screenshot hides: the label on a
// filled button, and the text on the user's bubble. Both look fine at a glance.
func TestPalettesAreReadable(t *testing.T) {
	type pair struct {
		what    string
		fg, bg  string
		minimum float64
	}
	var pairs []pair
	// Body text on every surface it can land on. Ink's worst is 8.0:1, on the
	// user's bubble; Paper's is 11.2:1.
	for _, fg := range []string{"astral_text", "window_fg_color"} {
		for _, bg := range []string{
			"astral_sidebar", "astral_canvas", "astral_surface", "astral_elevated",
			"astral_composer_bg", "astral_user_bubble", "card_bg_color", "dialog_bg_color", "popover_bg_color",
		} {
			pairs = append(pairs, pair{"body text", fg, bg, 7})
		}
	}
	pairs = append(pairs,
		// Ink 7.5:1 and 10.0:1, Paper 7.5:1 and 8.5:1.
		pair{"secondary text", "astral_muted", "astral_canvas", 4.5},
		pair{"secondary text", "astral_muted", "astral_surface", 4.5},
		// Ink 7.6:1 and 9.4:1, Paper 6.0:1 and 5.1:1.
		pair{"caption", "astral_faint", "astral_canvas", 4.5},
		pair{"caption", "astral_faint", "astral_sidebar", 4.5},
		// Ink 5.7:1 and Paper 5.7:1.
		pair{"button label", "accent_fg_color", "accent_bg_color", 4.5},
		pair{"delete button label", "destructive_fg_color", "destructive_bg_color", 4.5},
		// The accent as text: links, and the selected chat's title.
		pair{"accent text", "accent_color", "astral_canvas", 4.5},
		pair{"accent text", "accent_color", "astral_surface", 4.5},
		// And the colours libadwaita draws its own widgets with, in pairs.
		pair{"title bar text", "headerbar_fg_color", "headerbar_bg_color", 7},
		pair{"sidebar text", "sidebar_fg_color", "sidebar_bg_color", 7},
		pair{"view text", "view_fg_color", "view_bg_color", 7},
		pair{"card text", "card_fg_color", "card_bg_color", 7},
		pair{"dialog text", "dialog_fg_color", "dialog_bg_color", 7},
		pair{"popover text", "popover_fg_color", "popover_bg_color", 7},
		// The status colours are read as icons and short words, which WCAG holds to
		// 3:1. Ink's are over 7:1 and Paper's over 5.6:1, so this is the floor and
		// not what they reach.
		pair{"success", "success_color", "astral_canvas", 3},
		pair{"warning", "warning_color", "astral_canvas", 3},
		pair{"error", "error_color", "astral_canvas", 3},
	)
	// Secondary text on the other surfaces it sits on: a card's description, a
	// menu's hint, the composer's buttons. The caption colour is held to 3:1
	// there rather than 4.5:1: captions proper, times and counts, sit on the
	// canvas and the sidebar, checked above, and on a raised surface this
	// colour is a placeholder or a hint. Measured, Ink's faint is 4.05:1 on
	// the user's bubble and Ember's 4.41:1 on its composer.
	for _, bg := range []string{"astral_elevated", "astral_composer_bg", "astral_user_bubble", "card_bg_color", "popover_bg_color"} {
		pairs = append(pairs, pair{"secondary text", "astral_muted", bg, 4.5}, pair{"placeholder", "astral_faint", bg, 3})
	}
	pairs = append(pairs, pair{"accent text", "accent_color", "astral_sidebar", 4.5},
		pair{"accent text", "accent_color", "popover_bg_color", 4.5})
	for _, th := range Themes {
		for _, p := range pairs {
			fg, bg := th.Color(p.fg), th.Color(p.bg)
			if fg == "" || bg == "" {
				t.Errorf("%s: %s or %s is missing", th.ID, p.fg, p.bg)
				continue
			}
			if r := wcagContrast(t, fg, bg); r < p.minimum {
				t.Errorf("%s: %s is %.2f:1 (%s on %s), under the %.1f:1 minimum",
					th.ID, p.what, r, p.fg, p.bg, p.minimum)
			}
		}
	}
}

// TestLevelsCanBeSeen holds the ladder. Ink's steps are 1.24:1 (sidebar to
// canvas) and 1.34:1 (canvas to surface), Paper's 1.16:1 and 1.13:1, and its
// bubble is 1.34:1 from the character's.
//
// Paper's canvas to surface step is the one place it is under the 1.15:1 asked
// of it, at 1.135:1. That threshold is therefore 1.13:1 here, and the derived
// light themes are solved for 1.16:1, so they clear 1.15:1 in fact.
func TestLevelsCanBeSeen(t *testing.T) {
	for _, th := range Themes {
		steps := []struct {
			what, a, b string
			minimum    float64
		}{
			{"sidebar and canvas", "astral_sidebar", "astral_canvas", 1.15},
			{"canvas and surface", "astral_canvas", "astral_surface", 1.13},
			{"the user's bubble and the character's", "astral_user_bubble", "astral_surface", 1.3},
		}
		for _, s := range steps {
			if r := wcagContrast(t, th.Color(s.a), th.Color(s.b)); r < s.minimum {
				t.Errorf("%s: %s are %.3f:1 apart, under %.2f:1", th.ID, s.what, r, s.minimum)
			}
		}
	}
}

// TestLadderIsInOrder: the rungs are in order, whichever way they run. A dark
// theme climbs from its canvas to its surface, the composer and the borders; a
// light one, having no room above its canvas, steps down to its surface and its
// borders and puts the composer above the page.
func TestLadderIsInOrder(t *testing.T) {
	for _, th := range Themes {
		lum := func(name string) float64 { return relativeLuminance(t, th.Color(name)) }
		sidebar, canvas, surface, elevated, border := lum("astral_sidebar"), lum("astral_canvas"),
			lum("astral_surface"), lum("astral_elevated"), lum("astral_border")
		if th.Dark {
			if !(canvas < surface && surface < elevated && elevated < border) {
				t.Errorf("%s: canvas, surface, elevated and border should climb, got %.3f %.3f %.3f %.3f",
					th.ID, canvas, surface, elevated, border)
			}
			// Nothing is darker than a black canvas, so its sidebar is above it.
			if sidebar > canvas && canvas > 0.001 {
				t.Errorf("%s: the sidebar is lighter than the canvas, which has room to go darker", th.ID)
			}
		} else {
			if !(border < surface && surface < canvas && canvas < elevated) {
				t.Errorf("%s: border, surface, canvas and elevated should climb, got %.3f %.3f %.3f %.3f",
					th.ID, border, surface, canvas, elevated)
			}
			if sidebar >= canvas {
				t.Errorf("%s: the sidebar is not darker than the canvas", th.ID)
			}
		}
	}
}

// TestCSSCarriesBothSyntaxes is the one that would have shipped broken.
//
// libadwaita reads different colours through different mechanisms: the window
// background comes from the named colour @window_bg_color, while its own rule for
// links is `color: var(--accent-color)`. Emitting one form and not the other
// re-themes about half the window and leaves links on the desktop's accent.
func TestCSSCarriesBothSyntaxes(t *testing.T) {
	for _, th := range Themes {
		css := th.CSS()
		if !strings.Contains(css, ":root {") {
			t.Errorf("%s: no :root block, so nothing reading var(--accent-color) follows the theme", th.ID)
		}
		for _, name := range names {
			named := "@define-color " + name + " " + th.Color(name) + ";"
			if !strings.Contains(css, named) {
				t.Errorf("%s: %q is missing", th.ID, named)
			}
			variable := "--" + strings.ReplaceAll(name, "_", "-") + ": " + th.Color(name) + ";"
			if strings.HasPrefix(name, "astral_") {
				if strings.Contains(css, "--astral-") {
					t.Errorf("%s: Astral's own colour %s has a custom property, which nothing reads", th.ID, name)
				}
			} else if !strings.Contains(css, variable) {
				t.Errorf("%s: %q is missing from the :root block", th.ID, variable)
			}
		}
	}
}

// Every line of generated CSS is one of a handful of shapes, with no room for a
// stray character. GTK's parser is the real authority on whether a stylesheet
// loads, and internal/app checks these through it in a child process; this is the
// check that runs where there is no display, and it is strict on purpose.
var (
	themeLine = regexp.MustCompile(`^(/\* [A-Za-z ,.'/]+ \*/|:root \{|\}|` +
		`  --[a-z]+(-[a-z]+)*: #[0-9a-f]{6};|` +
		`@define-color [a-z]+(_[a-z]+)* #[0-9a-f]{6};)$`)
	swatchLine = regexp.MustCompile(`^(/\* [A-Za-z ,.'/]+ \*/|\}|` +
		`\.theme-swatch\.theme-swatch-[a-z]+ \{|` +
		`  background-image: linear-gradient\(135deg, (#[0-9a-f]{6} (0|50|100)%(, )?){4}\);)$`)
)

func TestGeneratedCSSIsWellFormed(t *testing.T) {
	check := func(what, css string, shape *regexp.Regexp) {
		t.Helper()
		open := 0
		for i, line := range strings.Split(strings.TrimRight(css, "\n"), "\n") {
			if !shape.MatchString(line) {
				t.Errorf("%s line %d is not a shape the generator writes: %q", what, i+1, line)
			}
			open += strings.Count(line, "{") - strings.Count(line, "}")
			if open < 0 || open > 1 {
				t.Errorf("%s line %d: unbalanced braces", what, i+1)
			}
		}
		if open != 0 {
			t.Errorf("%s ends with an open block", what)
		}
	}
	for _, th := range Themes {
		check(th.ID, th.CSS(), themeLine)
	}
	check("the swatches", SwatchCSS(), swatchLine)
}

// TestSwatchCSSCoversEveryTheme: the circles are generated from the table so that
// adding a theme cannot leave its circle blank. That only holds if this stays true.
func TestSwatchCSSCoversEveryTheme(t *testing.T) {
	css := SwatchCSS()
	for _, th := range Themes {
		selector := ".theme-swatch." + SwatchClass(th.ID) + " {"
		i := strings.Index(css, selector)
		if i < 0 {
			t.Errorf("no rule for %s", selector)
			continue
		}
		rule := css[i : i+strings.Index(css[i:], "}")]
		if !strings.Contains(rule, th.Canvas()) || !strings.Contains(rule, th.Accent()) {
			t.Errorf("%s: its canvas and accent are not in its rule", th.ID)
		}
	}
	// Both classes in the selector on purpose: one ties with Adwaita's own button
	// rules and the background-image is the whole widget.
	if strings.Contains(css, "\n.theme-swatch-") {
		t.Error("a swatch rule uses one class, which does not outrank Adwaita's button styling")
	}
}

// TestResolveFollowsTheDesktop: an unset setting, and one naming a theme this
// build does not have, both mean "follow the desktop" to Resolve, which draws Ink
// on a dark desktop and Paper on a light one. A theme that exists is used whatever
// the desktop is doing, because choosing one is the point at which Astral stops
// following.
func TestResolveFollowsTheDesktop(t *testing.T) {
	for _, id := range []string{Follow, "", "chartreuse", "Nord"} { // ids are case-sensitive
		if !IsFollowing(id) {
			t.Errorf("IsFollowing(%q) = false, want true", id)
		}
		if got := Resolve(id, true); got.ID != "ink" {
			t.Errorf("Resolve(%q, dark) = %q, want ink", id, got.ID)
		}
		if got := Resolve(id, false); got.ID != "paper" {
			t.Errorf("Resolve(%q, light) = %q, want paper", id, got.ID)
		}
	}
	for _, th := range Themes {
		if IsFollowing(th.ID) {
			t.Errorf("IsFollowing(%q) = true", th.ID)
		}
		for _, systemDark := range []bool{true, false} {
			if got := Resolve(th.ID, systemDark); got.ID != th.ID {
				t.Errorf("Resolve(%q, %v) = %q", th.ID, systemDark, got.ID)
			}
		}
	}
}

// TestNormalise: a settings file from before the themes says "dark" or "light",
// and one from a newer version may name a theme this build has not got.
func TestNormalise(t *testing.T) {
	for in, want := range map[string]string{
		"dark": "ink", "light": "paper", "system": Follow,
		"ink": "ink", "nord": "nord", "contrast": "contrast",
		"": Default, "chartreuse": Default, "Ink": Default,
	} {
		if got := Normalise(in); got != want {
			t.Errorf("Normalise(%q) = %q, want %q", in, got, want)
		}
	}
	// And what it returns is stable: normalising twice is normalising once.
	for _, th := range Themes {
		if got := Normalise(Normalise(th.ID)); got != th.ID {
			t.Errorf("Normalise is not stable on %q: %q", th.ID, got)
		}
	}
}

// TestContrastFormula is the calibration: a readability test built on a wrong
// formula passes every palette, so the formula is checked against values that are
// known. Black on white is 21:1 by definition, and the two mid greys are the
// standard worked examples.
func TestContrastFormula(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"#000000", "#ffffff", 21},
		{"#ffffff", "#ffffff", 1},
		{"#777777", "#ffffff", 4.48},
		{"#767676", "#ffffff", 4.54},
	} {
		if got := wcagContrast(t, c.a, c.b); math.Abs(got-c.want) > 0.01 {
			t.Errorf("%s on %s is %.2f:1, want %.2f:1", c.a, c.b, got, c.want)
		}
		// And the solver's own arithmetic agrees, which is what makes the
		// independent check above worth having.
		if got := contrastHex(c.a, c.b); math.Abs(got-c.want) > 0.01 {
			t.Errorf("the solver says %s on %s is %.2f:1, want %.2f:1", c.a, c.b, got, c.want)
		}
	}
}

// wcagContrast is WCAG 2.1's contrast ratio between two #rrggbb colours, written
// out here on its own rather than through the package's helper, so that a mistake
// in the formula the palettes were solved with cannot also excuse them.
func wcagContrast(t *testing.T, a, b string) float64 {
	t.Helper()
	la, lb := relativeLuminance(t, a), relativeLuminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relativeLuminance(t *testing.T, c string) float64 {
	t.Helper()
	n, err := strconv.ParseUint(strings.TrimPrefix(c, "#"), 16, 32)
	if err != nil || len(c) != 7 {
		t.Fatalf("parsing %q: %v", c, err)
	}
	channel := func(v uint64) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(n>>16&0xff) + 0.7152*channel(n>>8&0xff) + 0.0722*channel(n&0xff)
}
