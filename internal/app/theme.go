package app

import (
	"os"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/theme"
)

// Astral does not follow the desktop's grey. It has colours of its own, and every
// widget, including stock popovers and dialogs, inherits them because the theme
// redefines libadwaita's named colours. Which colours is a setting: the themes are
// in internal/theme, and Ink is the default.
//
// GTK CSS has no equivalent of prefers-color-scheme, so the themes cannot live in
// one stylesheet. Instead the structural CSS is loaded once and the colour
// definitions live in a second provider whose contents are replaced when the theme
// changes. Loading new contents into a provider re-resolves every @named colour in
// the first one, which is what makes a live theme switch possible at all.
type themer struct {
	structure *gtk.CSSProvider
	colors    *gtk.CSSProvider

	// loaded is the ID of the theme whose colours are in the provider now. Loading
	// a stylesheet restyles every widget in every window, and apply is called
	// whenever the setting is touched and whenever the desktop's own scheme
	// changes, so an unchanged theme must cost nothing rather than a full restyle
	// that changes nothing.
	loaded string
}

func newThemer() *themer { return &themer{} }

// install loads the structural stylesheet. Called once, at activation.
//
// The picker's circles are generated from the theme table and go in the same
// provider, so a theme cannot be added without its circle.
//
// ASTRAL_DEV_CSS is appended to it, which is how a rule can be tried without a
// rebuild, GTK's own layout is the only way to find out what a value actually
// renders as, and a five-minute gotk4 build per experiment makes that
// impractical otherwise.
func (t *themer) install(css string) {
	if css == "" {
		return
	}
	css += "\n" + theme.SwatchCSS()
	if extra := os.Getenv("ASTRAL_DEV_CSS"); extra != "" {
		css += "\n" + extra
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

// apply puts a setting into effect: a theme's ID, or theme.Follow.
//
// Two separate things, and both matter. The colour scheme decides whether
// libadwaita draws its light or its dark widgets, its symbolic icons and its
// scrollbars, and the colours then supply the palette itself. A theme that is dark
// under the light scheme would have dark icons on a dark page.
func (t *themer) apply(setting string) {
	sm := adw.StyleManagerGetDefault()
	if theme.IsFollowing(setting) {
		sm.SetColorScheme(adw.ColorSchemePreferDark)
		// "System" means whatever the desktop settled on, which libadwaita has
		// already worked out, so ask it rather than re-deriving it here.
		t.load(theme.Resolve(setting, sm.Dark()))
		return
	}
	th := theme.Resolve(setting, false)
	if th.Dark {
		sm.SetColorScheme(adw.ColorSchemeForceDark)
	} else {
		sm.SetColorScheme(adw.ColorSchemeForceLight)
	}
	t.load(th)
}

// load puts a theme's colours in the provider, unless they are already there.
func (t *themer) load(th theme.Theme) {
	if th.ID == t.loaded {
		return
	}
	display := gdk.DisplayGetDefault()
	if display == nil {
		return
	}
	first := t.colors == nil
	if first {
		t.colors = gtk.NewCSSProvider()
	}
	t.colors.LoadFromString(th.CSS())
	if first {
		// Loaded before it is added, so that the first theme is one restyle and
		// not an empty stylesheet followed by the real one.
		gtk.StyleContextAddProviderForDisplay(display, t.colors, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION+1)
	}
	t.loaded = th.ID
}

// watchSystem re-applies the theme when the desktop's preference changes, which
// only matters while the app is set to follow it.
func (t *themer) watchSystem(current func() string) {
	sm := adw.StyleManagerGetDefault()
	sm.NotifyProperty("dark", func() {
		if setting := current(); theme.IsFollowing(setting) {
			t.load(theme.Resolve(setting, sm.Dark()))
		}
	})
}
