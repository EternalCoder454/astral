package app

import (
	"log"
	"os"
	"path/filepath"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
	"astral/internal/ui"
)

// Uninstalling Astral from inside it.
//
// Removing a program on Linux means knowing how it was installed, and Astral
// is installed two ways: built from source into the home directory, which no
// package manager knows about, or as a Flatpak. Both can be removed from
// here, and the library is kept unless you say otherwise twice, since
// characters and scenes built up over months are the one thing here that
// cannot be downloaded again.

// confirmUninstall asks before removing Astral, and whether the library goes
// with it.
func (a *App) confirmUninstall() {
	d := adw.NewAlertDialog("Uninstall Astral?",
		"Astral will be removed from this computer and closed. Your characters, worlds, "+
			"chats and settings are kept, so installing it again brings them back.")
	del := gtk.NewCheckButton()
	del.SetChild(wrappingLabel("Also delete my library, which cannot be undone"))
	d.SetExtraChild(del)
	d.AddResponse("cancel", "Cancel")
	d.AddResponse("uninstall", "Uninstall")
	d.SetResponseAppearance("uninstall", adw.ResponseDestructive)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response != "uninstall" {
			return
		}
		if del.Active() {
			a.confirmDeleteLibrary()
		} else {
			a.uninstall(false)
		}
	})
	ui.FreeOnClose(&d.Dialog)
	d.Present(a.win)
}

// confirmDeleteLibrary is the second question, for the part that cannot be
// undone.
func (a *App) confirmDeleteLibrary() {
	d := adw.NewAlertDialog("Delete Your Library?",
		"Every character, world, chat, backup and setting will be deleted along with Astral. "+
			"This cannot be undone.")
	d.AddResponse("cancel", "Cancel")
	d.AddResponse("delete", "Delete and Uninstall")
	d.SetResponseAppearance("delete", adw.ResponseDestructive)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response == "delete" {
			a.uninstall(true)
		}
	})
	ui.FreeOnClose(&d.Dialog)
	d.Present(a.win)
}

// uninstall removes Astral, then closes it. The library, when it goes too, is
// deleted as Astral closes, after the database is shut, so nothing written on
// the way out puts any of it back.
func (a *App) uninstall(deleteLibrary bool) {
	a.toast("Uninstalling Astral…")
	go func() {
		var err error
		switch {
		case devRun():
			// A dev run's library is a temporary one, but the program files
			// are the real ones: ~/.local/bin is not moved by XDG_DATA_HOME.
			for _, p := range programFiles() {
				log.Printf("astral: uninstall: would remove %s", p)
			}
		case inFlatpak():
			err = uninstallFlatpak(deleteLibrary)
		default:
			err = removeProgramFiles()
		}
		coreglib.IdleAdd(func() bool {
			if err != nil {
				a.showUninstallFailed(err)
				return false
			}
			a.forgetLibrary = deleteLibrary
			a.adw.Quit()
			return false
		})
	}()
}

// showUninstallFailed says what went wrong, with what to try instead.
func (a *App) showUninstallFailed(err error) {
	body := err.Error()
	if inFlatpak() {
		body += "\n\nYou can also remove Astral from your software centre, or with " +
			"flatpak uninstall " + appID + " in a terminal."
	}
	d := adw.NewAlertDialog("Astral Was Not Uninstalled", body)
	d.AddResponse("close", "Close")
	ui.FreeOnClose(&d.Dialog)
	d.Present(a.win)
}

// libraryDirs are where the library lives: the database, portraits and
// backups, the settings, and cached thumbnails.
func libraryDirs() []string {
	dirs := []string{store.DataDir(), filepath.Dir(store.ConfigPath())}
	if cache, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(cache, store.AppName))
	}
	return dirs
}

// deleteLibrary removes the library, as Astral closes after an uninstall that
// asked for it. The database is closed by then.
func deleteLibrary() {
	for _, dir := range libraryDirs() {
		if devRun() {
			log.Printf("astral: uninstall: would remove %s", dir)
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("astral: uninstall: %v", err)
		}
	}
}
