//go:build !race

// Not under -race: this builds GTK widgets, and the race detector's pointer
// checks abort inside gotk4 as soon as GTK starts.

package ui

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/ollama"
)

// While a reply streams, finished paragraphs are frozen into labels of their
// own and only the last is rewritten; when it ends, it is one label with every
// word in it, as a reply loaded from the database is.
func TestStreamingFreezesParagraphs(t *testing.T) {
	// A display as well as GTK: in CI's container gtk_init_check reports
	// success with no display behind it, and the first widget made then
	// crashes the whole test binary.
	if !gtk.InitCheck() || gdk.DisplayGetDefault() == nil {
		t.Skip("no display to start GTK on")
	}
	m := NewMessageRow(MessageOpts{Role: ollama.RoleAssistant, Mode: Roleplay})
	m.BeginStreaming(400)
	for _, tok := range []string{"*She turns.* ", "\"First.\"", "\n\n", "*Then* ", "\"second.\"\n", "\n*And a third", " begins*"} {
		m.AppendText(tok)
	}
	if m.stream == nil {
		t.Fatal("no streaming labels")
	}
	var frozen []string
	children := 0
	for c := m.stream.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		children++
		if l, ok := c.(*gtk.Label); ok && l.Object.Native() != m.tail.Object.Native() {
			frozen = append(frozen, l.Text())
		}
	}
	if len(frozen) != 2 || frozen[0] != "*She turns.* \"First.\"" || frozen[1] != "*Then* \"second.\"" {
		t.Errorf("frozen %q", frozen)
	}
	if got := m.tail.Text(); got != "*And a third begins*" {
		t.Errorf("tail %q", got)
	}
	if m.body.Visible() {
		t.Error("the body showed while streaming")
	}
	// Filling its column while pinned, so GTK is not searching for its width
	// at each new height; see fillWhileStreaming.
	if a := m.bubble.HAlign(); a != gtk.AlignFill {
		t.Errorf("a streaming bubble aligned %v, not filling", a)
	}

	// A preamble taken back leaves nothing behind.
	m.ClearStreamed()
	left := 0
	for c := m.stream.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		left++
	}
	if left != 1 || m.tail.Text() != "" {
		t.Errorf("after clearing: %d labels, tail %q", left, m.tail.Text())
	}
	m.AppendText("Only this.")
	m.EndStreaming()
	if m.stream != nil || !m.body.Visible() || m.Text() != "Only this." {
		t.Errorf("after the end: stream %v, body visible %v, text %q", m.stream != nil, m.body.Visible(), m.Text())
	}
	if a := m.bubble.HAlign(); a != gtk.AlignStart {
		t.Errorf("a finished reply aligned %v, so it would not shrink to its text", a)
	}
}

// A reply too narrow to pin keeps to its side while it streams, since its
// width is still negotiated; a reply continued fills like a new one; and
// ending twice, as a group scene's rows can be, leaves it aligned as it was.
func TestStreamingAlignment(t *testing.T) {
	if !gtk.InitCheck() || gdk.DisplayGetDefault() == nil {
		t.Skip("no display to start GTK on")
	}
	narrow := NewMessageRow(MessageOpts{Role: ollama.RoleAssistant, Mode: Roleplay})
	narrow.BeginStreaming(0)
	if a := narrow.bubble.HAlign(); a != gtk.AlignStart {
		t.Errorf("an unpinned reply aligned %v while streaming", a)
	}
	narrow.EndStreaming()

	cont := NewMessageRow(MessageOpts{Role: ollama.RoleAssistant, Mode: Roleplay})
	cont.SetMarkdown("*She waits.*")
	cont.ContinueStreaming(400)
	if a := cont.bubble.HAlign(); a != gtk.AlignFill {
		t.Errorf("a continued reply aligned %v while streaming", a)
	}
	cont.AppendText(" \"Well?\"")
	cont.EndStreaming()
	cont.EndStreaming()
	if a := cont.bubble.HAlign(); a != gtk.AlignStart {
		t.Errorf("a continued reply aligned %v once finished", a)
	}
}
