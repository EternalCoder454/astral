package app

import (
	"context"
	"os"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/transcript"
)

// actionExportChat writes a scene out as Markdown.
//
// A roleplay is writing, and writing that exists only inside one application's
// database is writing you do not really have.
func (a *App) actionExportChat(id int64) {
	ch, err := a.store.Chat(id)
	if err != nil {
		a.toast("Could not read that chat: " + err.Error())
		return
	}
	msgs, err := a.store.Messages(id)
	if err != nil {
		a.toast("Could not read the messages: " + err.Error())
		return
	}
	who := ch.CharacterName
	if who == "" && ch.WorldID != 0 {
		if w, err := a.store.World(ch.WorldID); err == nil {
			who = w.Name
		}
	}
	data := transcript.Markdown(ch, msgs, who, a.cfg.PersonaName)

	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Export scene")
	dialog.SetInitialName(transcript.Filename(ch) + ".md")
	dialog.Save(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		file, err := dialog.SaveFinish(res)
		if err != nil || file == nil {
			return // cancelled, which is not worth a message
		}
		if err := os.WriteFile(file.Path(), []byte(data), 0o644); err != nil {
			a.toast("Could not write the file: " + err.Error())
			return
		}
		a.toast("Exported.")
	})
}
