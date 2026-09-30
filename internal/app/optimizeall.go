package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/promptopt"
	"astral/internal/prompts"
	"astral/internal/scene"
	"astral/internal/store"
	"astral/internal/ui"
)

// Optimize All: the Prompt Optimizer run over every prompt, one at a time, with
// the model you chat with, in the background. Nothing is saved until you have
// looked at what it wrote and chosen what to keep.

// promptBatch is a run of Optimize All.
type promptBatch struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	total    int
	current  string
	results  []promptopt.Rewrite
	finished bool
	model    string
}

func (b *promptBatch) status() (done, total int, current string, finished bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.results), b.total, b.current, b.finished
}

// startOptimizeAll begins a run, unless one is going.
func (a *App) startOptimizeAll() {
	if a.batch != nil {
		if _, _, _, finished := a.batch.status(); !finished {
			return
		}
	}
	model := a.cfg.Model
	if model == "" {
		a.toast("Choose a model first.")
		return
	}
	var all []prompts.Prompt
	for _, p := range prompts.All() {
		if !p.List {
			all = append(all, p)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &promptBatch{cancel: cancel, total: len(all), model: model}
	a.batch = b
	client, opts := a.client, scene.OptionsFor(a.cfg, store.KindPromptOptimizer)

	go func() {
		defer cancel()
		// The same model the chat uses, and nothing beside it: whatever was
		// used for replies before is released first.
		client.UseForReplies(ctx, model)
		for _, p := range all {
			if ctx.Err() != nil {
				break
			}
			b.mu.Lock()
			b.current = p.Name
			b.mu.Unlock()
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Minute)
			r := promptopt.RewriteOne(pctx, client, model, opts, p.ID)
			pcancel()
			if ctx.Err() != nil {
				break
			}
			b.mu.Lock()
			b.results = append(b.results, r)
			b.mu.Unlock()
		}
		b.mu.Lock()
		b.finished, b.current = true, ""
		stopped := ctx.Err() != nil
		n := len(b.results)
		b.mu.Unlock()
		coreglib.IdleAdd(func() bool {
			ready := 0
			for _, r := range b.results {
				if r.Err == nil && !r.Unchanged {
					ready++
				}
			}
			msg := fmt.Sprintf("Optimize All finished: %d rewrites to review.", ready)
			if stopped {
				msg = fmt.Sprintf("Optimize All stopped after %d prompts: %d rewrites to review.", n, ready)
			}
			a.toastAction(msg, "Review", a.showOptimizeReview)
			return false
		})
	}()
}

// optimizeAllRow is the line at the top of the Prompts page: the button to
// start, how far a run has got with a way to stop it, or the results waiting.
// It keeps itself up to date while it is on screen.
func (a *App) optimizeAllRow() *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 10)
	row.AddCSSClass("card-row")
	label := wrappingLabel("")
	label.SetHExpand(true)
	label.AddCSSClass("settings-hint")
	row.Append(label)
	button := gtk.NewButton()
	row.Append(button)

	var handler coreglib.SignalHandle
	refresh := func() {
		if handler != 0 {
			button.HandlerDisconnect(handler)
		}
		b := a.batch
		if b == nil {
			label.SetText("Optimize every prompt in turn, keeping only what you approve.")
			button.SetLabel("Optimize All")
			button.AddCSSClass("suggested-action")
			handler = button.ConnectClicked(func() { a.startOptimizeAll() })
			return
		}
		done, total, current, finished := b.status()
		if !finished {
			label.SetText(fmt.Sprintf("Optimizing %d of %d: %s", done+1, total, current))
			button.SetLabel("Stop")
			button.RemoveCSSClass("suggested-action")
			handler = button.ConnectClicked(func() { b.cancel() })
			return
		}
		label.SetText(fmt.Sprintf("Optimize All went through %d prompts, ready for review.", done))
		button.SetLabel("Review")
		button.AddCSSClass("suggested-action")
		handler = button.ConnectClicked(a.showOptimizeReview)
	}
	refresh()
	coreglib.TimeoutAdd(1000, func() bool {
		// A page that is not showing is still in the window, so the timer runs
		// until the row is let go of, and only does its work while it is seen.
		if row.Root() == nil {
			return false
		}
		if row.Mapped() {
			refresh()
		}
		return true
	})
	return row
}

// showOptimizeReview lists what Optimize All wrote, to choose what to keep.
func (a *App) showOptimizeReview() {
	b := a.batch
	if b == nil {
		return
	}
	b.mu.Lock()
	results := append([]promptopt.Rewrite(nil), b.results...)
	b.mu.Unlock()

	d := adw.NewDialog()
	ui.FreeOnClose(d)
	d.SetTitle("Review the Rewrites")
	d.SetContentWidth(720)
	d.SetContentHeight(760)

	chosen := map[string]*gtk.CheckButton{}
	after := map[string]string{}
	header := saveHeader(d, "Use the rewrites that are ticked", func() bool {
		saved := 0
		for _, r := range results {
			cb := chosen[r.ID]
			if cb == nil || !cb.Active() {
				continue
			}
			if err := a.store.SetPromptOverride(r.ID, after[r.ID]); err != nil {
				a.toast("Could not save " + r.Name + ": " + err.Error())
				return false
			}
			saved++
		}
		a.loadPromptOverrides()
		a.refreshPage(pagePrompts)
		a.toast(fmt.Sprintf("Astral sends %d rewritten %s from now on.", saved, plural(saved, "prompt", "prompts")))
		return true
	})

	page := gtk.NewBox(gtk.OrientationVertical, 10)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)
	intro := wrappingLabel("Written by " + b.model + ", and only the ones you tick are saved.")
	intro.AddCSSClass("settings-hint")
	page.Append(intro)

	for _, r := range results {
		r := r
		card := gtk.NewBox(gtk.OrientationVertical, 4)
		card.AddCSSClass("card-row")
		top := gtk.NewBox(gtk.OrientationHorizontal, 8)
		cb := gtk.NewCheckButton()
		cb.SetLabel(r.Name)
		cb.SetHExpand(true)
		top.Append(cb)
		card.Append(top)

		var note string
		switch {
		case r.Err != nil:
			note = "No rewrite: " + r.Err.Error()
			cb.SetSensitive(false)
		case r.Unchanged:
			note = "Already as good as it could make it."
			cb.SetSensitive(false)
		default:
			chosen[r.ID] = cb
			after[r.ID] = r.After
			// Nothing starts ticked. Astral's own prompts were tuned by
			// measurement, and a rewrite that reads better is as likely as not
			// to have lost something no check can see: comparing is the point.
			cb.SetActive(false)
			note = r.Summary()
			compare := gtk.NewButtonWithLabel("Compare")
			compare.ConnectClicked(func() { a.comparePrompt(r) })
			top.Append(compare)
		}
		if note != "" {
			l := wrappingLabel(note)
			l.AddCSSClass("settings-hint")
			card.Append(l)
		}
		for _, p := range r.Problems {
			w := wrappingLabel(p)
			w.AddCSSClass("warning-hint")
			card.Append(w)
		}
		page.Append(card)
	}

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolled(page))
	d.SetChild(tv)
	d.Present(a.win)
}

// comparePrompt shows a prompt as it is sent now above the rewrite of it.
func (a *App) comparePrompt(r promptopt.Rewrite) {
	d := adw.NewDialog()
	ui.FreeOnClose(d)
	d.SetTitle(r.Name)
	d.SetContentWidth(760)
	d.SetContentHeight(800)
	page := gtk.NewBox(gtk.OrientationVertical, 12)
	page.SetMarginTop(14)
	page.SetMarginBottom(14)
	page.SetMarginStart(14)
	page.SetMarginEnd(14)
	for _, part := range []struct{ label, text string }{
		{"Now", r.Before},
		{"Rewrite", r.After},
		{"What It Said", strings.TrimSpace(r.Reply)},
	} {
		frame, tv := multilineField(part.text, 8)
		tv.SetEditable(false)
		page.Append(labelledField(part.label, "", frame))
	}
	header := adw.NewHeaderBar()
	tvw := adw.NewToolbarView()
	tvw.AddTopBar(header)
	tvw.SetContent(scrolled(page))
	d.SetChild(tvw)
	d.Present(a.win)
}
