package app

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/ui"
)

// The portrait panel shows the character you are playing with beside the
// scene. It is a side panel rather than something in the transcript because a
// portrait is scenery: it should stay put while the conversation scrolls, and
// it should be possible to put it away.

// buildPortraitPanel constructs the panel. Its contents are filled in by
// refreshPortrait whenever a chat opens.
func (a *App) buildPortraitPanel() *gtk.Box {
	a.portraitBox = gtk.NewBox(gtk.OrientationVertical, 10)
	a.portraitBox.AddCSSClass("portrait-panel")
	a.portraitBox.SetSizeRequest(200, -1)
	return a.portraitBox
}

// refreshPortrait fills the panel for a character, and reports whether there
// was anything worth showing.
func (a *App) refreshPortrait(c chars.Character) bool {
	if a.portraitBox == nil {
		return false
	}
	for {
		child := a.portraitBox.FirstChild()
		if child == nil {
			break
		}
		a.portraitBox.Remove(child)
	}
	if c.Name == "" {
		return false
	}

	if pic := ui.NewPortrait(c.PortraitPath); pic != nil {
		a.portraitBox.Append(pic)
	} else {
		// No portrait: the avatar, shown large, is better than an empty panel
		// and still says who you are talking to.
		big := ui.NewCharacterAvatar(c, 120)
		gtk.BaseWidget(big).SetVAlign(gtk.AlignCenter)
		gtk.BaseWidget(big).SetVExpand(true)
		a.portraitBox.Append(big)
	}

	name := gtk.NewLabel(c.Name)
	name.SetWrap(true)
	name.SetJustify(gtk.JustifyCenter)
	name.AddCSSClass("portrait-name")
	a.portraitBox.Append(name)

	if sum := ui.Snippet(c.Summary(), 160); sum != "" {
		desc := gtk.NewLabel(sum)
		desc.SetWrap(true)
		desc.SetJustify(gtk.JustifyCenter)
		desc.SetXAlign(0.5)
		desc.AddCSSClass("portrait-desc")
		a.portraitBox.Append(desc)
	}
	return true
}

// showPortraitFor decides whether the panel is available for this scene and
// whether it should currently be open.
//
// It is only offered for a character scene: there is nothing to show in a
// plain chat or a design session, and a toggle that opens an empty panel is
// worse than no toggle.
func (a *App) showPortraitFor(c chars.Character) {
	has := a.refreshPortrait(c)
	if a.portraitBtn != nil {
		a.portraitBtn.SetVisible(has)
	}
	if a.portraitSplit != nil {
		open := has && a.cfg.PortraitOpen
		a.portraitSplit.SetShowSidebar(open)
		if a.portraitBtn != nil {
			a.portraitBtn.SetActive(open)
		}
	}
}
