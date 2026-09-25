package app

import (
	"os"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
)

// Astral does not follow the desktop's grey. Its whole point is to look like
// Claude Desktop, so it redefines libadwaita's named colours to Anthropic's
// palette and every widget — including stock popovers and dialogs — inherits
// the warm scheme.
//
// GTK CSS has no equivalent of prefers-color-scheme, so the two schemes cannot
// live in one stylesheet. Instead the structural CSS is loaded once and the
// colour definitions live in a second provider that is swapped when the scheme
// changes. Swapping a provider re-resolves every @named colour in the first
// one, which is what makes a live theme switch possible at all.
type themer struct {
	structure *gtk.CSSProvider
	colors    *gtk.CSSProvider
	dark      string
	light     string

	// installed tracks whether the colour provider is currently attached, so
	// a swap removes the old one rather than stacking a second on top.
	installed bool
}

func newThemer(structure, dark, light string) *themer {
	return &themer{dark: dark, light: light}
}

// install loads the structural stylesheet. Called once, at activation.
//
// ASTRAL_DEV_CSS is appended to it, which is how a rule can be tried without a
// rebuild — GTK's own layout is the only way to find out what a value actually
// renders as, and a five-minute gotk4 build per experiment makes that
// impractical otherwise.
func (t *themer) install(css string) {
	if extra := os.Getenv("ASTRAL_DEV_CSS"); extra != "" {
		css += "\n" + extra
	}
	if css == "" {
		return
	}
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	t.structure = gtk.NewCSSProvider()
	t.structure.LoadFromString(css)
	// The colour provider is added at a higher priority than the structural
	// one below, so a @define-color always wins over anything the structure
	// file might set.
	gtk.StyleContextAddProviderForDisplay(display, t.structure, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
}

// apply sets the colour scheme, both for libadwaita (so its own stylesheet
// picks the right variant) and for our colour provider.
func (t *themer) apply(theme string) {
	sm := adw.StyleManagerGetDefault()
	dark := theme == store.ThemeDark
	switch theme {
	case store.ThemeDark:
		sm.SetColorScheme(adw.ColorSchemeForceDark)
	case store.ThemeLight:
		sm.SetColorScheme(adw.ColorSchemeForceLight)
	default:
		sm.SetColorScheme(adw.ColorSchemePreferDark)
		// "System" means whatever the desktop settled on, which libadwaita has
		// already worked out — so ask it rather than re-deriving it here.
		dark = sm.Dark()
	}
	t.loadColors(dark)
}

func (t *themer) loadColors(dark bool) {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	if t.installed && t.colors != nil {
		gtk.StyleContextRemoveProviderForDisplay(display, t.colors)
		t.installed = false
	}
	css := t.light
	if dark {
		css = t.dark
	}
	if css == "" {
		return
	}
	t.colors = gtk.NewCSSProvider()
	t.colors.LoadFromString(css)
	gtk.StyleContextAddProviderForDisplay(display, t.colors, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION+1)
	t.installed = true
}

// watchSystem re-applies the scheme when the desktop's preference changes,
// which only matters while the app is set to follow it.
func (t *themer) watchSystem(current func() string) {
	sm := adw.StyleManagerGetDefault()
	sm.NotifyProperty("dark", func() {
		if current() == store.ThemeSystem {
			t.loadColors(sm.Dark())
		}
	})
}
