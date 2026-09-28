package ui

import (
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// rewriteNotes are the notes asked for most, one click each. Anything else is
// typed.
var rewriteNotes = []string{"Shorter", "Longer", "More Dialogue", "More Detail", "Less Formal"}

// AskRewriteNote asks what a reply should do differently, and hands the answer
// to rewrite.
//
// A note, not an instruction to the scene: it applies to the one reply being
// written again and is gone after it. What should keep applying is a Direction.
func AskRewriteNote(parent gtk.Widgetter, rewrite func(note string)) {
	d := adw.NewDialog()
	FreeOnClose(d)
	d.SetTitle("Rewrite with a Note")
	d.SetContentWidth(520)

	page := gtk.NewBox(gtk.OrientationVertical, 12)
	page.SetMarginTop(16)
	page.SetMarginBottom(16)
	page.SetMarginStart(16)
	page.SetMarginEnd(16)

	entry := gtk.NewEntry()
	entry.SetPlaceholderText("She refuses, more about the storm, end on a question…")
	entry.SetActivatesDefault(false)
	page.Append(entry)

	chips := gtk.NewFlowBox()
	chips.SetSelectionMode(gtk.SelectionNone)
	chips.SetMaxChildrenPerLine(6)
	chips.SetColumnSpacing(6)
	chips.SetRowSpacing(6)
	pick := func(note string) {
		note = strings.TrimSpace(note)
		if note == "" {
			return
		}
		d.Close()
		rewrite(note)
	}
	for _, n := range rewriteNotes {
		n := n
		b := gtk.NewButtonWithLabel(n)
		b.AddCSSClass("chat-action-chip")
		b.ConnectClicked(func() { pick(n) })
		chips.Append(b)
	}
	page.Append(chips)

	hint := gtk.NewLabel("For this reply only. To steer the scene from here on, set a Direction.")
	hint.SetXAlign(0)
	hint.SetWrap(true)
	hint.AddCSSClass("settings-hint")
	page.Append(hint)

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	header.SetShowStartTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)
	ok := gtk.NewButtonWithLabel("Rewrite")
	ok.AddCSSClass("suggested-action")
	ok.SetSensitive(false)
	ok.ConnectClicked(func() { pick(entry.Text()) })
	header.PackEnd(ok)
	entry.ConnectChanged(func() { ok.SetSensitive(strings.TrimSpace(entry.Text()) != "") })
	entry.ConnectActivate(func() { pick(entry.Text()) })

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(page)
	d.SetChild(tv)
	d.Present(parent)
	entry.GrabFocus()
}
