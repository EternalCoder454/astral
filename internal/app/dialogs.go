package app

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"strings"
)

// confirm asks before something irreversible. The confirming button is styled
// destructive and is never the default, so Enter cannot delete anything.
func (a *App) confirm(heading, body, confirmLabel string, onConfirm func()) {
	d := adw.NewAlertDialog(heading, body)
	d.AddResponse("cancel", "Cancel")
	d.AddResponse("confirm", confirmLabel)
	d.SetResponseAppearance("confirm", adw.ResponseDestructive)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response == "confirm" {
			onConfirm()
		}
	})
	d.Present(a.win)
}

// promptText asks for a single line of text.
func (a *App) promptText(heading, label, initial string, onAccept func(string)) {
	d := adw.NewAlertDialog(heading, "")
	entry := gtk.NewEntry()
	entry.SetText(initial)
	entry.SetHExpand(true)
	entry.SetActivatesDefault(true) // Enter accepts, rather than doing nothing

	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.SetMarginTop(4)
	l := gtk.NewLabel(label)
	l.SetXAlign(0)
	l.AddCSSClass("field-label")
	box.Append(l)
	box.Append(entry)
	d.SetExtraChild(box)

	d.AddResponse("cancel", "Cancel")
	d.AddResponse("ok", "Save")
	d.SetResponseAppearance("ok", adw.ResponseSuggested)
	d.SetDefaultResponse("ok")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response == "ok" {
			onAccept(entry.Text())
		}
	})
	d.Present(a.win)
	entry.GrabFocus()
}

// showAbout is the standard about window.
func (a *App) showAbout() {
	about := adw.NewAboutDialog()
	about.SetApplicationName("Astral")
	about.SetApplicationIcon(appID)
	about.SetVersion(version)
	about.SetComments("A world roleplay system for local language models. " +
		"Build characters, set a scene, and play it out. Everything runs on this machine, " +
		"and nothing you write is sent anywhere.")
	about.SetWebsite(projectURL)
	about.SetIssueURL(projectURL + "/issues")
	about.SetLicenseType(gtk.LicenseMITX11)
	about.Present(a.win)
}

// shortcut is one row in the shortcuts window.
type shortcut struct{ keys, what string }

// showShortcuts lists the keyboard shortcuts.
func (a *App) showShortcuts() {
	groups := []struct {
		title string
		items []shortcut
	}{
		{"Chat", []shortcut{
			{"Enter", "Send the message"},
			{"Shift+Enter", "Start a new line"},
			{"Ctrl+L", "Jump to the message box"},
			{"Ctrl+N", "New chat"},
		}},
		{"Navigate", []shortcut{
			{"Ctrl+K", "Characters"},
			{"Ctrl+M", "Choose a model"},
			{"F9", "Show or hide the sidebar"},
			{"Ctrl+,", "Settings"},
			{"Ctrl+/", "This list"},
		}},
	}

	page := gtk.NewBox(gtk.OrientationVertical, 18)
	page.SetMarginTop(18)
	page.SetMarginBottom(18)
	page.SetMarginStart(18)
	page.SetMarginEnd(18)

	for _, g := range groups {
		heading := gtk.NewLabel(g.title)
		heading.SetXAlign(0)
		heading.AddCSSClass("settings-heading")
		page.Append(heading)

		card := gtk.NewBox(gtk.OrientationVertical, 10)
		card.AddCSSClass("settings-group")
		for _, it := range g.items {
			row := gtk.NewBox(gtk.OrientationHorizontal, 12)
			what := gtk.NewLabel(it.what)
			what.SetXAlign(0)
			what.SetHExpand(true)
			row.Append(what)
			key := gtk.NewLabel(it.keys)
			key.AddCSSClass("keycap")
			row.Append(key)
			card.Append(row)
		}
		page.Append(card)
	}

	d := adw.NewDialog()
	d.SetTitle("Keyboard Shortcuts")
	d.SetContentWidth(420)
	d.SetContentHeight(460)

	header := adw.NewHeaderBar()
	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// scrolled wraps a page so it can be scrolled vertically.
//
// Horizontal scrolling is deliberately off: with it on, a page becomes as wide
// as its widest unwrappable label, and long text is then clipped instead of
// wrapping.
func scrolled(child gtk.Widgetter) *gtk.ScrolledWindow {
	s := gtk.NewScrolledWindow()
	s.SetChild(child)
	s.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	s.SetVExpand(true)
	return s
}

// fitToContentHeight is the tallest a self-sizing dialog will ask to be. Past
// this it scrolls, which is what the scrolled window was for.
const fitToContentHeight = 560

// scrolledToFit is scrolled() for a dialog with no fixed height.
//
// A GtkScrolledWindow reports a natural height of nothing, so a dialog that
// asks its child how tall to be gets nothing back and presents as a title bar
// with the form clipped off under it. This reports the height the content
// wants, up to a limit. Use it for a short dialog; use scrolled with an
// explicit SetContentHeight otherwise, which the test next door requires.
func scrolledToFit(child gtk.Widgetter) *gtk.ScrolledWindow {
	s := scrolled(child)
	s.SetPropagateNaturalHeight(true)
	s.SetMaxContentHeight(fitToContentHeight)
	return s
}

// groupCard is the bordered container settings and dialogs group fields into.
func groupCard(title string) (*gtk.Box, *gtk.Box) {
	outer := gtk.NewBox(gtk.OrientationVertical, 6)
	if title != "" {
		l := gtk.NewLabel(title)
		l.SetXAlign(0)
		l.AddCSSClass("settings-heading")
		outer.Append(l)
	}
	card := gtk.NewBox(gtk.OrientationVertical, 12)
	card.AddCSSClass("settings-group")
	outer.Append(card)
	return outer, card
}

// saveHeader is the Cancel and Save bar an editing dialog carries.
//
// onSave returns whether the dialog is finished: false leaves it open, which a
// validation failure needs, having just put the reason in a toast nobody would
// see if the dialog closed underneath it. Putting that contract in the
// signature is the point of the helper.
//
// saveTip, where set, spells out what Save covers.
func saveHeader(d *adw.Dialog, saveTip string, onSave func() bool) *adw.HeaderBar {
	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)
	save := gtk.NewButtonWithLabel("Save")
	save.AddCSSClass("suggested-action")
	save.SetTooltipText(saveTip)
	save.ConnectClicked(func() {
		if onSave() {
			d.Close()
		}
	})
	header.PackEnd(save)
	return header
}

// labelledField is a caption above a widget, with an optional hint below.
func labelledField(label, hint string, child gtk.Widgetter) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	if label != "" {
		l := gtk.NewLabel(label)
		l.SetXAlign(0)
		l.AddCSSClass("field-label")
		box.Append(l)
	}
	box.Append(child)
	if hint != "" {
		h := gtk.NewLabel(hint)
		h.SetXAlign(0)
		h.SetWrap(true)
		h.AddCSSClass("settings-hint")
		box.Append(h)
	}
	return box
}

// cardDescription is the two-line summary under a name on a card: a world's
// description, a character's. Cards in a grid have to be the same height
// whatever the text, or the grid comes out ragged, so this is two lines,
// ellipsized, left aligned. The few places that want three lines, or no cap at
// all, still say so themselves.
func cardDescription(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	l.SetWrap(true)
	l.SetLines(2)
	l.SetEllipsize(pango.EllipsizeEnd)
	l.AddCSSClass("character-card-desc")
	return l
}

// multilineField is a bordered text area, sized to a minimum number of lines.
func multilineField(text string, minLines int) (*gtk.Frame, *gtk.TextView) {
	tv := gtk.NewTextView()
	tv.SetWrapMode(gtk.WrapWordChar)
	tv.SetTopMargin(6)
	tv.SetBottomMargin(6)
	tv.SetLeftMargin(6)
	tv.SetRightMargin(6)
	tv.Buffer().SetText(text)
	tv.SetSizeRequest(-1, minLines*22)

	frame := gtk.NewFrame("")
	frame.SetChild(tv)
	return frame, tv
}

// textOf reads a text view's whole buffer.
func textOf(tv *gtk.TextView) string {
	b := tv.Buffer()
	start, end := b.Bounds()
	return b.Text(start, end, false)
}

// searchThreshold is how many rows a list needs before it is offered a search
// field. Below this the field is pure clutter: scanning four names is faster
// than reaching for the keyboard, and an empty search box over an empty list
// is the clearest way to make a new install look complicated.
const searchThreshold = 7

// addRow is the "make another one" button that sits at the end of a list.
//
// The header keeps its "+" for people who know where it is. This is for
// everyone else: a short list above a large empty panel gives no indication
// that anything can be added to it, and the panel is emptiest exactly when a
// new user is looking at it.
func addRow(label string, onClick func()) *gtk.Button {
	b := gtk.NewButton()
	b.AddCSSClass("add-row")
	b.SetChild(rowLabel("+   " + label))
	b.ConnectClicked(onClick)
	return b
}

func rowLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetXAlign(0)
	return l
}

// filterRow is one row of a searchable list, with the text it matches on.
type filterRow struct {
	Widget gtk.Widgetter
	// Text is everything worth matching, already lowercased: a name, its
	// description, its tags. Matching the description as well as the name is
	// what makes searching a lorebook useful, since the thing you remember
	// about an entry is rarely its title.
	Text string
}

// searchableList fills a box with rows and, when there are enough of them,
// puts a search field above that filters as you type.
//
// Filtering hides rows rather than rebuilding the list, so the widgets and
// their signal handlers are built once and a keystroke costs a visibility
// change per row rather than a teardown.
func searchableList(list *gtk.Box, placeholder string, rows []filterRow) {
	if len(rows) >= searchThreshold {
		search := gtk.NewSearchEntry()
		search.SetPlaceholderText(placeholder)
		search.SetHExpand(true)
		search.SetMarginBottom(4)
		list.Append(search)

		empty := gtk.NewLabel("Nothing matches that.")
		empty.SetJustify(gtk.JustifyCenter)
		empty.SetVExpand(true)
		empty.SetVisible(false)
		empty.AddCSSClass("dim-label")

		search.ConnectSearchChanged(func() {
			q := strings.ToLower(strings.TrimSpace(search.Text()))
			shown := 0
			for _, r := range rows {
				match := q == "" || strings.Contains(r.Text, q)
				gtk.BaseWidget(r.Widget).SetVisible(match)
				if match {
					shown++
				}
			}
			empty.SetVisible(shown == 0)
		})
		for _, r := range rows {
			list.Append(r.Widget)
		}
		list.Append(empty)
		return
	}
	for _, r := range rows {
		list.Append(r.Widget)
	}
}
