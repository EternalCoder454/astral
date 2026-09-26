package app

import (
	"context"
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/world"
)

// Filling a lorebook from something already written.
//
// It could only be done two ways before: one entry at a time by hand, or learned
// from play. Neither helps the person whose setting is already written down
// somewhere and who wants it in the app, which is most people with a world worth
// having.

// showLoreFromText asks for a document and turns it into entries.
func (a *App) showLoreFromText(w world.World, onDone func()) {
	d := adw.NewDialog()
	d.SetTitle("Read Lore from Text")
	d.SetContentWidth(620)

	page := settingsPage()
	outer, card := groupCard("")

	hint := wrappingLabel("Paste anything you have written about this world: notes, a " +
		"wiki page, a document. The model reads it and writes one entry per subject, " +
		"with the words a conversation would have to mention for each to be sent.")
	hint.AddCSSClass("settings-hint")
	card.Append(hint)

	frame, view := multilineField("", 12)
	card.Append(frame)
	page.Append(outer)

	status := wrappingLabel("")
	status.AddCSSClass("settings-hint")
	page.Append(status)

	header := adw.NewHeaderBar()
	header.SetShowEndTitleButtons(false)
	cancel := gtk.NewButtonWithLabel("Cancel")
	cancel.ConnectClicked(func() { d.Close() })
	header.PackStart(cancel)

	read := gtk.NewButtonWithLabel("Read It")
	read.AddCSSClass("suggested-action")
	header.PackEnd(read)

	read.ConnectClicked(func() {
		text := textOf(view)
		model := a.cfg.HousekeepingModel
		if model == "" {
			model = a.cfg.Model
		}
		if model == "" {
			status.SetText("Choose a model in Settings first.")
			return
		}
		read.SetSensitive(false)
		read.SetLabel("Reading…")
		status.SetText("")

		client := a.client
		opts := ollama.Options{NumCtx: a.cfg.NumCtx}
		go func() {
			ctx, cancelCtx := context.WithTimeout(context.Background(), buildTimeout)
			defer cancelCtx()
			entries, err := world.EntriesFromText(ctx, client, model, text, opts)

			coreglib.IdleAdd(func() bool {
				read.SetSensitive(true)
				read.SetLabel("Read It")
				if err != nil {
					status.SetText(err.Error())
					return false
				}
				kept, held := 0, 0
				for _, e := range entries {
					e.WorldID = w.ID
					if _, err := a.store.SaveLoreEntry(e); err != nil {
						// A hand-written entry refusing an automatic update is
						// the intended behaviour, not a failure.
						if err != store.ErrWouldOverwriteManual {
							held++
						}
						continue
					}
					kept++
				}
				d.Close()
				switch {
				case held > 0:
					a.toast(fmt.Sprintf("Added %d entries to %s. %d were left alone because you had written them yourself.",
						kept, w.Name, held))
				default:
					a.toast(fmt.Sprintf("Added %d entries to %s.", kept, w.Name))
				}
				if onDone != nil {
					onDone()
				}
				return false
			})
		}()
	})

	tv := adw.NewToolbarView()
	tv.AddTopBar(header)
	tv.SetContent(scrolledToFit(page))
	d.SetChild(tv)
	d.Present(a.win)
}
