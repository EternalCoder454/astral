package app

import (
	"context"
	"fmt"
	"os"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/world"
)

// Getting a world in and out of the app.
//
// Characters have had this from the beginning, because a character card is a
// format other applications already speak. A world had nothing: it lived inside
// one SQLite file with every other world, so the only way to keep a copy of a
// setting was to keep a copy of the whole database. A world someone spends weeks
// on should be something they own.

// maxWorldFile bounds what will be read. A world is text, and the largest real
// one is a few hundred kilobytes; anything past this is either not a world or is
// not one this app should be loading into memory to find out.
const maxWorldFile = 8 << 20

// exportWorld writes a world and its lorebook to a file the user chooses.
func (a *App) exportWorld(w world.World) {
	entries, err := a.store.LoreEntries(w.ID)
	if err != nil {
		a.toast("Could not read the lorebook: " + err.Error())
		return
	}
	data, err := world.Encode(w, entries, version)
	if err != nil {
		a.toast("Could not write the world: " + err.Error())
		return
	}

	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Export World")
	dialog.SetInitialName(world.Filename(w) + ".world.json")
	dialog.Save(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		file, err := dialog.SaveFinish(res)
		if err != nil || file == nil {
			return // cancelled, which is not worth a message
		}
		path := file.Path()
		if err := os.WriteFile(path, data, 0o644); err != nil {
			a.toast("Could not save it: " + err.Error())
			return
		}
		a.toast(fmt.Sprintf("%s saved, with %d lorebook entries.", w.Name, len(entries)))
	})
}

// actionImportWorld reads a world file and saves it as a new world.
func (a *App) actionImportWorld() {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Import a World")

	worlds := gtk.NewFileFilter()
	worlds.SetName("Astral Worlds")
	worlds.AddPattern("*.world.json")
	worlds.AddPattern("*.json")
	all := gtk.NewFileFilter()
	all.SetName("All Files")
	all.AddPattern("*")

	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(worlds.Object)
	filters.Append(all.Object)
	dialog.SetFilters(filters)
	dialog.SetDefaultFilter(worlds)

	dialog.Open(context.Background(), &a.win.Window, func(res gio.AsyncResulter) {
		file, err := dialog.OpenFinish(res)
		if err != nil || file == nil {
			return
		}
		a.importWorldFile(file.Path())
	})
}

// importWorldFile reads one world file.
//
// It is always a new world rather than a merge into an existing one. A merge has
// to answer what happens to an entry that exists in both, and every answer to
// that is wrong for somebody; two worlds side by side can be compared and one of
// them deleted, which is a decision the person can actually make.
func (a *App) importWorldFile(path string) {
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		a.toast("Could not open that file: " + err.Error())
		return
	}
	if info.Size() > maxWorldFile {
		a.toast("That file is too large to be a world.")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		a.toast("Could not read that file: " + err.Error())
		return
	}
	draft, err := world.Decode(data)
	if err != nil {
		a.toast(err.Error())
		return
	}
	a.saveWorldDraft(draft)
}
