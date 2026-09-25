package app

import (
	"fmt"
	"os/user"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/ui"
)

// The welcome screen is what you see with no chat open. It has one job beyond
// looking like the front of an app: to tell you, honestly, whether Astral can
// actually do anything right now — and if not, exactly what to run.

// buildWelcome constructs the home screen. Its contents are rebuilt by
// refreshWelcome whenever the model probe or the cast changes.
func (a *App) buildWelcome() *gtk.Widget {
	page := gtk.NewBox(gtk.OrientationVertical, 0)
	page.AddCSSClass("welcome-page")

	a.welcomeBox = gtk.NewBox(gtk.OrientationVertical, 0)
	a.welcomeBox.SetVAlign(gtk.AlignCenter)
	a.welcomeBox.SetMarginTop(24)
	a.welcomeBox.SetMarginBottom(24)

	// A clamp, not a size request. The welcome page shares a GtkStack with the
	// chat, and a stack's minimum width is the widest of its children — so a
	// hard 560px here became a 560px floor on the whole window, and no amount
	// of fixing the transcript could make the app narrower than the page you
	// are not even looking at.
	welcomeClamp := adw.NewClamp()
	welcomeClamp.SetMaximumSize(560)
	welcomeClamp.SetTighteningThreshold(460)
	welcomeClamp.SetChild(a.welcomeBox)

	scroll := scrolled(welcomeClamp)
	page.Append(scroll)
	a.welcome = &page.Widget
	a.refreshWelcome()
	return &page.Widget
}

// refreshWelcome rebuilds the home screen's contents.
func (a *App) refreshWelcome() {
	if a.welcomeBox == nil {
		return
	}
	for {
		child := a.welcomeBox.FirstChild()
		if child == nil {
			break
		}
		a.welcomeBox.Remove(child)
	}

	orb := ui.NewOrb(76)
	orb.SetMarginBottom(6)
	a.welcomeBox.Append(orb)

	title := gtk.NewLabel(a.greetingLine())
	title.AddCSSClass("welcome-title")
	title.SetHAlign(gtk.AlignCenter)
	a.welcomeBox.Append(title)

	sub := gtk.NewLabel("Roleplay with anyone, on your own machine.")
	sub.AddCSSClass("welcome-subtitle")
	sub.SetHAlign(gtk.AlignCenter)
	a.welcomeBox.Append(sub)

	// When Ollama cannot answer, that is the only thing worth showing: a grid
	// of characters you cannot talk to yet would just be a dead end.
	if !a.ready() {
		a.welcomeBox.Append(a.buildSetupCard())
		return
	}
	a.appendCast()
}

// greetingLine addresses you by name when the system knows it, the way the
// desktop's own greeter does.
func (a *App) greetingLine() string {
	name := a.cfg.PersonaName
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = strings.TrimSpace(u.Name)
			if name == "" {
				name = u.Username
			}
			// A full name in a greeting reads as a form letter; the first word
			// reads as a greeting.
			if i := strings.IndexByte(name, ' '); i > 0 {
				name = name[:i]
			}
		}
	}
	if name == "" {
		return "Welcome to Astral"
	}
	return "Welcome back, " + name
}

// appendCast lists the characters you can start a scene with.
func (a *App) appendCast() {
	characters, err := a.store.Characters()
	if err != nil {
		a.toast("Could not read your characters: " + err.Error())
		return
	}

	heading := gtk.NewLabel("Start a scene")
	heading.SetXAlign(0)
	heading.AddCSSClass("welcome-section")
	a.welcomeBox.Append(heading)

	if len(characters) == 0 {
		a.welcomeBox.Append(a.buildEmptyCast())
		return
	}

	// A flow box so the cards reflow with the window rather than being pinned
	// to a fixed number of columns.
	flow := gtk.NewFlowBox()
	flow.SetSelectionMode(gtk.SelectionNone)
	flow.SetMaxChildrenPerLine(2)
	flow.SetColumnSpacing(10)
	flow.SetRowSpacing(10)
	flow.SetHomogeneous(true)

	const maxShown = 6
	shown := characters
	if len(shown) > maxShown {
		shown = shown[:maxShown]
	}
	for _, c := range shown {
		flow.Insert(a.characterCard(c), -1)
	}
	a.welcomeBox.Append(flow)

	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.SetHAlign(gtk.AlignCenter)
	row.SetMarginTop(12)

	design := gtk.NewButton()
	design.AddCSSClass("sidebar-item")
	design.SetLabel("Design a new character")
	design.SetTooltipText("Describe what you want and the model builds it with you")
	design.ConnectClicked(a.newDesignerChat)
	row.Append(design)

	plain := gtk.NewButton()
	plain.AddCSSClass("sidebar-item")
	plain.SetLabel("Just chat")
	plain.SetTooltipText("A plain conversation with the model, no character")
	plain.ConnectClicked(a.newAssistantChat)
	row.Append(plain)

	more := gtk.NewButton()
	more.AddCSSClass("sidebar-item")
	if len(characters) > maxShown {
		more.SetLabel(fmt.Sprintf("All %d…", len(characters)))
	} else {
		more.SetLabel("Manage")
	}
	more.SetTooltipText("Browse, edit and import characters")
	more.ConnectClicked(a.showCharacters)
	row.Append(more)

	a.welcomeBox.Append(row)
}

// buildEmptyCast is the first-run state: no characters yet.
func (a *App) buildEmptyCast() *gtk.Box {
	outer, card := groupCard("")
	t := gtk.NewLabel("No characters yet")
	t.SetXAlign(0)
	t.AddCSSClass("setup-title")
	card.Append(t)

	b := gtk.NewLabel("Let the model build one with you: describe what you want and it asks the rest. " +
		"You can also import a character card you already have, or write one yourself.")
	b.SetXAlign(0)
	b.SetWrap(true)
	b.AddCSSClass("setup-body")
	card.Append(b)

	// The designer is the prominent offer. Facing eight empty text boxes is
	// where most people give up, and being asked "who are they?" is a far
	// easier way in.
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.SetHAlign(gtk.AlignStart)
	design := gtk.NewButtonWithLabel("Design one with the model")
	design.AddCSSClass("suggested-action")
	design.ConnectClicked(a.newDesignerChat)
	row.Append(design)
	imp := gtk.NewButtonWithLabel("Import a card…")
	imp.ConnectClicked(a.actionImportCharacter)
	row.Append(imp)
	write := gtk.NewButtonWithLabel("Write one")
	write.ConnectClicked(func() { a.editCharacter(chars.Character{}) })
	row.Append(write)
	card.Append(row)
	return outer
}

// characterCard is one clickable character on the home screen.
func (a *App) characterCard(c chars.Character) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("character-card")

	box := gtk.NewBox(gtk.OrientationHorizontal, 10)
	avatar := ui.NewAvatar(c.Initial(), c.Accent, 36)
	avatar.SetVAlign(gtk.AlignStart)
	box.Append(avatar)

	col := gtk.NewBox(gtk.OrientationVertical, 2)
	col.SetHExpand(true)
	name := gtk.NewLabel(c.Name)
	name.SetXAlign(0)
	name.SetEllipsize(3)
	name.AddCSSClass("character-card-name")
	col.Append(name)

	desc := gtk.NewLabel(ui.Snippet(c.Summary(), 70))
	desc.SetXAlign(0)
	desc.SetWrap(true)
	desc.SetLines(2)
	desc.SetEllipsize(3)
	desc.AddCSSClass("character-card-desc")
	col.Append(desc)
	box.Append(col)

	btn.SetChild(box)
	btn.SetTooltipText("Start a scene with " + c.Name)
	character := c
	btn.ConnectClicked(func() { a.newChat(character) })
	return btn
}

// buildSetupCard explains why Astral cannot talk to a model yet, and gives the
// exact command to fix it. Which of the three states it shows is the whole
// point: "Ollama is not running" and "you have no models" need different
// answers, and guessing wrong wastes your time.
func (a *App) buildSetupCard() *gtk.Box {
	outer, card := groupCard("")

	title := gtk.NewLabel("")
	title.SetXAlign(0)
	title.AddCSSClass("setup-title")
	body := gtk.NewLabel("")
	body.SetXAlign(0)
	body.SetWrap(true)
	body.AddCSSClass("setup-body")

	var cmd string
	switch {
	case a.probeErr != nil:
		title.SetText("Ollama isn't running")
		body.SetText(fmt.Sprintf("Astral talks to a model server on this machine. Start it, then press Retry.\n\nTried: %s", a.cfg.BaseURL))
		cmd = "ollama serve"
	case len(a.models) == 0:
		title.SetText("No models installed")
		body.SetText("Ollama is running but has nothing to run. Pull a model to get started, this one is a good balance of quality and size for roleplay.")
		cmd = "ollama pull qwen3:8b"
	case a.cfg.Model != "" && !ollama.HasModel(a.models, a.cfg.Model):
		// Configured, but no longer there. Naming it matters: the difference
		// between "pull it back" and "pick another" is which one you wanted.
		title.SetText(a.cfg.Model + " isn't installed")
		body.SetText("Astral is set to use a model Ollama no longer has. Pull it back, or choose one of the " +
			fmt.Sprintf("%d", len(a.models)) + " you do have.")
		cmd = "ollama pull " + a.cfg.Model
	default:
		title.SetText("No model selected")
		body.SetText("Pick which of your installed models Astral should use.")
	}
	card.Append(title)
	card.Append(body)

	if cmd != "" {
		c := gtk.NewLabel(cmd)
		c.SetXAlign(0)
		c.SetSelectable(true)
		c.AddCSSClass("setup-cmd")
		card.Append(c)
	}

	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.SetHAlign(gtk.AlignStart)
	if cmd != "" {
		copyBtn := gtk.NewButtonWithLabel("Copy command")
		copyBtn.ConnectClicked(func() {
			if d := gtk.BaseWidget(a.win).Display(); d != nil {
				d.Clipboard().SetText(cmd)
				a.toast("Copied, paste it into a terminal.")
			}
		})
		row.Append(copyBtn)
	}
	retry := gtk.NewButtonWithLabel("Retry")
	retry.AddCSSClass("suggested-action")
	retry.ConnectClicked(func() {
		a.toast("Checking for Ollama…")
		a.probeModels()
	})
	row.Append(retry)
	if a.probeErr == nil && len(a.models) > 0 {
		pick := gtk.NewButtonWithLabel("Choose a model")
		pick.ConnectClicked(a.showModelPicker)
		row.Append(pick)
	}
	card.Append(row)
	return outer
}
