//go:build !race

package ui

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/ollama"
)

// The floor under how narrow a message wraps goes only under a line at least
// that long, or a short message's bubble would widen to the floor.
func TestAFloorOnlyUnderLongLines(t *testing.T) {
	if !gtk.InitCheck() || gdk.DisplayGetDefault() == nil {
		t.Skip("no display to start GTK on")
	}
	m := NewMessageRow(MessageOpts{Role: ollama.RoleUser, Mode: Roleplay})
	for _, c := range []struct {
		text string
		want int
	}{
		{"Yes.", -1},
		{"Fine, then. A pause.", -1},
		{"*She nods, and says nothing more for a while, watching the harbour.*", bodyMinChars},
		{"Yes.\nNo.\nPerhaps.", -1},
		{"", -1},
	} {
		m.SetMarkdown(c.text)
		if got := m.body.WidthChars(); got != c.want {
			t.Errorf("%q: width chars %d, want %d", c.text, got, c.want)
		}
	}
}
