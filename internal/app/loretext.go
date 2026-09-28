package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/ollama"
	"astral/internal/scene"
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

	hint := wrappingLabel("Paste notes about this world and the model writes an entry per subject.")
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
		sceneModel := a.cfg.Model
		opts := ollama.Options{NumCtx: a.cfg.NumCtx}
		go func() {
			ctx, cancelCtx := context.WithTimeout(context.Background(), buildTimeout)
			defer cancelCtx()
			model := scene.FitHousekeeping(ctx, client, model, sceneModel)
			entries, err := world.EntriesFromText(ctx, client, model, text, opts)

			coreglib.IdleAdd(func() bool {
				read.SetSensitive(true)
				read.SetLabel("Read It")
				if err != nil {
					status.SetText(err.Error())
					return false
				}
				kept, held, failed := 0, 0, 0
				for _, e := range entries {
					e.WorldID = w.ID
					if _, err := a.store.SaveLoreEntry(e); err != nil {
						// A hand-written entry refusing an automatic update is
						// the intended behaviour, not a failure. The two were
						// counted the wrong way round: the message about
						// entries you wrote yourself counted real failures,
						// and the refusals went uncounted.
						if errors.Is(err, store.ErrWouldOverwriteManual) {
							held++
						} else {
							failed++
						}
						continue
					}
					kept++
				}
				d.Close()
				switch {
				case failed > 0:
					a.toast(fmt.Sprintf("Added %d entries to %s, but %d could not be saved.", kept, w.Name, failed))
				case held > 0:
					a.toast(fmt.Sprintf("Added %d entries to %s, and left %d you wrote yourself alone.",
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
