package app

import (
	"log"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/promptopt"
	"astral/internal/prompts"
	"astral/internal/store"
	"astral/internal/ui"
)

// The Prompts page and the Prompt Optimizer.
//
// Every prompt Astral sends a model is listed here, and each can be read,
// changed by hand, or handed to the optimizer, which is a design chat whose
// product is a better prompt. What you save is used everywhere that prompt is
// sent, and the original can always be put back.

// loadPromptOverrides hands your saved prompts to the prompts package, which is
// what every prompt is read through.
func (a *App) loadPromptOverrides() {
	if a.store == nil {
		return
	}
	m, err := a.store.PromptOverrides()
	if err != nil {
		log.Printf("astral: reading your prompts: %v", err)
		return
	}
	prompts.SetOverrides(m)
}

// showPrompts lists every prompt, grouped by what uses it.
func (a *App) showPrompts() {
	d := adw.NewDialog()
	ui.FreeOnClose(d)
	d.SetTitle("Prompts")
	d.SetContentWidth(640)
	d.SetContentHeight(720)

	header := adw.NewHeaderBar()
	bring := gtk.NewButtonFromIconName(ui.IconAdd)
	bring.SetTooltipText("Bring a prompt of your own to the Prompt Optimizer")
	bring.ConnectClicked(func() {
		d.Close()
		a.startPromptOptimizer("")
	})
	header.PackEnd(bring)

	page := gtk.NewBox(gtk.OrientationVertical, 10)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)

	about := wrappingLabel("Every prompt Astral sends, which you can edit or optimize.")
	about.AddCSSClass("settings-hint")
	page.Append(about)
	page.Append(a.optimizeAllRow(d))

	search := gtk.NewSearchEntry()
	search.SetPlaceholderText("Search the prompts")
	page.Append(search)

	list := gtk.NewBox(gtk.OrientationVertical, 8)
	page.Append(list)

	fill := func(query string) {
		for child := list.FirstChild(); child != nil; child = list.FirstChild() {
			list.Remove(child)
		}
		q := strings.ToLower(strings.TrimSpace(query))
		group := ""
		shown := 0
		for _, p := range prompts.All() {
			if q != "" && !strings.Contains(strings.ToLower(p.Name+" "+p.Group+" "+p.About+" "+prompts.Text(p.ID)), q) {
				continue
			}
			if p.Group != group {
				group = p.Group
				h := gtk.NewLabel(group)
				h.SetXAlign(0)
				h.AddCSSClass("settings-heading")
				list.Append(h)
			}
			list.Append(a.promptCard(p, d))
			shown++
		}
		if shown == 0 {
			empty := gtk.NewLabel("Nothing matches that.")
			empty.AddCSSClass("dim-label")
			empty.SetMarginTop(24)
			list.Append(empty)
		}
		if q == "" {
			list.Append(addRow("Optimize a Prompt of Your Own", func() {
				d.Close()
				a.startPromptOptimizer("")
			}))
		}
	}
	fill("")
	search.ConnectSearchChanged(func() { fill(search.Text()) })

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// promptCard is one prompt in the list.
func (a *App) promptCard(p prompts.Prompt, parent *adw.Dialog) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("character-card")

	col := gtk.NewBox(gtk.OrientationVertical, 3)
	head := gtk.NewBox(gtk.OrientationHorizontal, 8)
	title := gtk.NewLabel(p.Name)
	title.SetXAlign(0)
	title.SetHExpand(true)
	title.SetEllipsize(pango.EllipsizeEnd)
	title.AddCSSClass("character-card-name")
	head.Append(title)
	if prompts.Overridden(p.ID) {
		tag := gtk.NewLabel("Yours")
		tag.AddCSSClass("character-card-tag")
		tag.SetTooltipText("Your rewritten version is the one sent")
		head.Append(tag)
	}
	col.Append(head)
	col.Append(cardDescription(p.About))
	btn.SetChild(col)
	btn.SetTooltipText("Open " + p.Name)
	id := p.ID
	btn.ConnectClicked(func() {
		parent.Close()
		a.editPrompt(id)
	})
	return btn
}

// editPrompt shows one prompt as it is sent, to read, change by hand, or hand
// to the optimizer.
func (a *App) editPrompt(id string) {
	p, ok := prompts.Get(id)
	if !ok {
		return
	}
	d := adw.NewDialog()
	ui.FreeOnClose(d)
	d.SetTitle(p.Name)
	d.SetContentWidth(700)
	d.SetContentHeight(780)

	frame, body := multilineField(prompts.Text(id), 18)
	frame.SetVExpand(true)
	body.AddCSSClass("prompt-text")

	header := saveHeader(d, "Use this version of the prompt", func() bool {
		return a.savePrompt(id, textOf(body))
	})

	page := gtk.NewBox(gtk.OrientationVertical, 14)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)

	what := wrappingLabel(p.About)
	what.AddCSSClass("settings-hint")
	page.Append(what)
	if k := strings.TrimSpace(p.Keep); k != "" {
		keep := wrappingLabel("Keep: " + k)
		keep.AddCSSClass("settings-hint")
		page.Append(keep)
	}

	state := "Astral's own version."
	if prompts.Overridden(id) {
		state = "Your version, which is the one sent."
	}
	page.Append(labelledField("Text", state, frame))

	buttons := gtk.NewBox(gtk.OrientationHorizontal, 8)
	optimize := gtk.NewButtonWithLabel("Optimize…")
	optimize.AddCSSClass("suggested-action")
	optimize.SetTooltipText("Talk it over with the Prompt Optimizer")
	optimize.ConnectClicked(func() {
		d.Close()
		a.startPromptOptimizer(id)
	})
	buttons.Append(optimize)

	copyBtn := gtk.NewButtonWithLabel("Copy")
	copyBtn.ConnectClicked(func() {
		if disp := gdk.DisplayGetDefault(); disp != nil {
			disp.Clipboard().SetText(textOf(body))
			a.toast("Copied.")
		}
	})
	buttons.Append(copyBtn)

	if prompts.Overridden(id) {
		reset := gtk.NewButtonWithLabel("Put the Original Back")
		reset.AddCSSClass("destructive-action")
		reset.ConnectClicked(func() {
			a.confirm("Put the Original "+p.Name+" Back",
				"Your version is deleted and Astral's own is used again.",
				"Put It Back", func() {
					if err := a.store.DeletePromptOverride(id); err != nil {
						a.toast("Could not put it back: " + err.Error())
						return
					}
					a.loadPromptOverrides()
					d.Close()
					a.toast("Astral's own " + p.Name + " is back.")
				})
		})
		buttons.Append(reset)
	}
	page.Append(buttons)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// savePrompt stores your version of a prompt. The same text as Astral's is not
// a version of it, so saving that puts the original back instead.
func (a *App) savePrompt(id, text string) bool {
	p, ok := prompts.Get(id)
	if !ok {
		return false
	}
	text = strings.TrimSpace(text)
	var err error
	switch {
	case text == "":
		a.toast("An empty prompt sends nothing, so put the original back instead.")
		return false
	case text == strings.TrimSpace(p.Default):
		err = a.store.DeletePromptOverride(id)
	default:
		err = a.store.SetPromptOverride(id, text)
	}
	if err != nil {
		a.toast("Could not save the prompt: " + err.Error())
		return false
	}
	a.loadPromptOverrides()
	a.toast("Astral sends your " + p.Name + " from now on.")
	return true
}

// startPromptOptimizer opens a conversation about one prompt, or about one you
// bring when id is empty.
func (a *App) startPromptOptimizer(id string) {
	title := "Optimizing a Prompt"
	if p, ok := prompts.Get(id); ok {
		title = "Optimizing " + p.Name
	}
	a.chat.Clear()
	a.chat.LoadChat(store.Chat{
		Model: a.cfg.Model,
		Kind:  store.KindPromptOptimizer,
		Note:  id,
		Title: title,
	}, chars.Character{}, nil)
	a.chat.ShowGreeting(promptopt.Opening(id))
	a.showChat()
	a.sidebar.Select(0)
	a.setTitle(store.Chat{Title: title}, chars.Character{})
	a.chat.FocusComposer()
	a.refreshAttachAvailability()
}

// savePromptFromChat takes the prompt from the optimizer's latest reply that
// has one, and opens it for review before it is used.
func (a *App) savePromptFromChat() {
	proposal, ok := latestProposal(a.chat.History())
	if !ok {
		a.toast("No reply has a finished prompt yet, so ask for a rewrite.")
		return
	}
	id := strings.TrimSpace(a.chat.Chat().Note)
	p, known := prompts.Get(id)
	if !known {
		// A prompt you brought is yours to put wherever it goes.
		if disp := gdk.DisplayGetDefault(); disp != nil {
			disp.Clipboard().SetText(proposal)
		}
		a.toast("Copied the prompt.")
		return
	}

	d := adw.NewDialog()
	ui.FreeOnClose(d)
	d.SetTitle("Save " + p.Name)
	d.SetContentWidth(700)
	d.SetContentHeight(760)
	frame, body := multilineField(proposal, 18)
	frame.SetVExpand(true)
	body.AddCSSClass("prompt-text")
	header := saveHeader(d, "Use this version from now on", func() bool {
		return a.savePrompt(id, textOf(body))
	})

	page := gtk.NewBox(gtk.OrientationVertical, 12)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)
	hint := wrappingLabel("The optimizer's latest prompt, which you can edit before saving.")
	hint.AddCSSClass("settings-hint")
	page.Append(hint)
	for _, problem := range promptopt.ProblemsFor(p, proposal) {
		warn := wrappingLabel(problem)
		warn.AddCSSClass("warning-hint")
		page.Append(warn)
	}
	page.Append(frame)

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// latestProposal is the prompt in the newest reply that has one.
func latestProposal(history []ollama.Message) (string, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != ollama.RoleAssistant {
			continue
		}
		if p, ok := promptopt.Proposal(history[i].Content); ok {
			return p, true
		}
	}
	return "", false
}
