package ui

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// AccentCount is how many character tints the stylesheet defines.
const AccentCount = 8

// AccentFor derives a stable tint from a character's name, so the same
// character keeps the same colour across machines without it being stored —
// and so importing a folder of cards produces a varied cast rather than eight
// shades of clay. FNV-1a because it is three lines and spreads short strings
// well; nothing here needs a cryptographic hash.
func AccentFor(name string) int {
	const (
		offset = 2166136261
		prime  = 16777619
	)
	h := uint32(offset)
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= prime
	}
	return int(h % AccentCount)
}

// NewAvatar builds a character's avatar: a tinted rounded square carrying the
// first letter of their name.
//
// It is a styled label rather than a drawn widget so the tint comes from the
// stylesheet (one `.accent-N` class) instead of from code — which means the
// palette can be edited in CSS without recompiling, and the avatar inherits
// font rendering from the rest of the interface.
func NewAvatar(initial string, accent, size int) *gtk.Label {
	if initial == "" {
		initial = "?"
	}
	l := gtk.NewLabel(initial)
	l.AddCSSClass("avatar")
	l.AddCSSClass(accentClass(accent))
	l.SetSizeRequest(size, size)
	l.SetHAlign(gtk.AlignCenter)
	l.SetVAlign(gtk.AlignCenter)
	return l
}

// NewUserAvatar is the round clay disc standing in for you.
func NewUserAvatar(initial string, size int) *gtk.Label {
	if initial == "" {
		initial = "Y"
	}
	l := gtk.NewLabel(initial)
	l.AddCSSClass("avatar")
	l.AddCSSClass("avatar-user")
	l.SetSizeRequest(size, size)
	l.SetHAlign(gtk.AlignCenter)
	l.SetVAlign(gtk.AlignCenter)
	return l
}

// accentClass maps an accent index to its stylesheet class, wrapping rather
// than clamping so a value from an older database (or a larger palette) still
// lands on a real colour instead of all piling onto the last one.
func accentClass(accent int) string {
	if accent < 0 {
		accent = -accent
	}
	switch accent % AccentCount {
	case 1:
		return "accent-1"
	case 2:
		return "accent-2"
	case 3:
		return "accent-3"
	case 4:
		return "accent-4"
	case 5:
		return "accent-5"
	case 6:
		return "accent-6"
	case 7:
		return "accent-7"
	default:
		return "accent-0"
	}
}

// SetAccent swaps a widget's tint class, removing whichever one it had.
func SetAccent(w interface {
	AddCSSClass(string)
	RemoveCSSClass(string)
}, accent int) {
	for i := 0; i < AccentCount; i++ {
		w.RemoveCSSClass(accentClass(i))
	}
	w.AddCSSClass(accentClass(accent))
}
