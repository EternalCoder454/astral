package app

import (
	"log"
	"os"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
)

// GTK 4 renders text unhinted with grayscale antialiasing and does not round
// font metrics. On a HiDPI screen that is invisible and correct. At scale 1 —
// an ordinary 1080p monitor — it makes glyphs soft and line spacing uneven
// next to everything else on the desktop. Astral picks per display, and the
// setting lets you overrule it.
const hidpiThreshold = 2

// GtkFontRendering values (GTK 4.16+). gotk4 has no binding for the enum, so
// the numeric values are used directly.
const (
	fontRenderingAutomatic = 0
	fontRenderingManual    = 1
)

// applyFontRendering tunes text rendering for the display Astral is on.
func (a *App) applyFontRendering() {
	settings := gtk.SettingsGetDefault()
	if settings == nil {
		return
	}
	mode := a.cfg.FontRendering
	if mode == "" || mode == store.FontRenderingAuto {
		if a.lowDensityWindow() {
			mode = store.FontRenderingCrisp
		} else {
			mode = store.FontRenderingSmooth
		}
	}
	if mode == a.fontMode {
		return // already applied; re-applying churns every widget's layout
	}
	a.fontMode = mode

	switch mode {
	case store.FontRenderingCrisp:
		// GTK 4.16+ decides hinting for itself unless rendering is set to
		// manual; without this the xft settings below are silently ignored.
		setSetting(settings, "gtk-font-rendering", fontRenderingManual)
		setSetting(settings, "gtk-hint-font-metrics", true)
		setSetting(settings, "gtk-xft-antialias", 1)
		setSetting(settings, "gtk-xft-hinting", 1)
		if style := desktopHintStyle(settings); style != "" {
			setSetting(settings, "gtk-xft-hintstyle", style)
		}
	case store.FontRenderingSmooth:
		setSetting(settings, "gtk-font-rendering", fontRenderingAutomatic)
		setSetting(settings, "gtk-hint-font-metrics", false)
	}
	debugFontSettings(settings, mode)
}

// lowDensityWindow reports whether the screen the window is on needs hinted
// text. Once the window exists its own surface answers exactly, which matters
// on a mixed setup: dragging between a HiDPI and a 1080p screen re-runs this.
func (a *App) lowDensityWindow() bool {
	if a.win == nil {
		return lowDensityDisplay()
	}
	native := gtk.BaseWidget(a.win).Native()
	if native == nil {
		return lowDensityDisplay()
	}
	surface := native.Surface()
	if surface == nil {
		return lowDensityDisplay()
	}
	return gdk.BaseSurface(surface).ScaleFactor() < hidpiThreshold
}

// lowDensityDisplay is the answer before there is a window to ask about.
func lowDensityDisplay() bool {
	display := gdk.DisplayGetDefault()
	if display == nil {
		return true
	}
	monitors := display.Monitors()
	n := monitors.NItems()
	if n == 0 {
		return true
	}
	for i := uint(0); i < n; i++ {
		obj := monitors.Item(i)
		if obj == nil {
			continue
		}
		monitor, ok := obj.Cast().(*gdk.Monitor)
		if !ok {
			continue
		}
		if monitor.ScaleFactor() < hidpiThreshold {
			return true
		}
	}
	return false
}

// watchScaleChanges re-applies the settings when the window moves to a screen
// with a different pixel density — only while the mode is automatic.
func (a *App) watchScaleChanges() {
	if a.win == nil || a.cfg.FontRendering != store.FontRenderingAuto {
		return
	}
	native := gtk.BaseWidget(a.win).Native()
	if native == nil {
		return
	}
	surface := native.Surface()
	if surface == nil {
		return
	}
	gdk.BaseSurface(surface).NotifyProperty("scale-factor", func() {
		a.applyFontRendering()
	})
}

// desktopHintStyle returns the hint style the desktop already asks for, so the
// app matches the rest of the session rather than imposing its own taste. It
// falls back to slight hinting, GNOME's default.
func desktopHintStyle(settings *gtk.Settings) string {
	if v, ok := settings.ObjectProperty("gtk-xft-hintstyle").(string); ok && v != "" && v != "hintnone" {
		return v
	}
	return "hintslight"
}

// setSetting writes a GtkSettings property, logging rather than failing when a
// GTK version does not have it.
func setSetting(settings *gtk.Settings, name string, value any) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("astral: font setting %s unavailable: %v", name, r)
		}
	}()
	settings.SetObjectProperty(name, value)
}

// debugFontSettings prints the resolved settings when ASTRAL_DEBUG_FONTS is
// set — the first thing to check on a report of soft or uneven text.
func debugFontSettings(settings *gtk.Settings, mode string) {
	if os.Getenv("ASTRAL_DEBUG_FONTS") == "" {
		return
	}
	log.Printf("astral: font rendering mode=%s", mode)
	for _, name := range []string{
		"gtk-font-rendering", "gtk-hint-font-metrics", "gtk-xft-antialias",
		"gtk-xft-hinting", "gtk-xft-hintstyle", "gtk-xft-rgba", "gtk-xft-dpi",
		"gtk-font-name",
	} {
		log.Printf("  %-22s %v", name, settings.ObjectProperty(name))
	}
}
