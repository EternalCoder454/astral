package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
)

// Putting a daily backup back.
//
// Restoring meant quitting and copying a file over astral.db by hand, which
// is the one step in the app where a slip loses everything. Now it is chosen
// from a list, and done while Astral is closing, when nothing has the library
// open. The library it replaces is moved aside rather than deleted, and the
// next launch says where it went.

// restoredNote is the file the next launch reads to say a restore happened.
func restoredNote() string { return filepath.Join(filepath.Dir(store.DefaultDBPath()), "restored.txt") }

// showRestoreBackup lists the daily copies to choose one from.
func (a *App) showRestoreBackup() {
	backups := store.Backups(store.BackupDir())
	if len(backups) == 0 {
		a.toast("There are no backups yet; the first is made today.")
		return
	}
	d := adw.NewAlertDialog("Restore a Backup", "Astral closes to put it back, and keeps your current library beside it.")
	list := gtk.NewBox(gtk.OrientationVertical, 4)
	var group *gtk.CheckButton
	picked := backups[0].Path
	for _, b := range backups {
		b := b
		radio := gtk.NewCheckButton()
		radio.SetLabel(fmt.Sprintf("%s (%s)", backupDay(b.Day), sizeOf(b.Size)))
		if group == nil {
			group = radio
			radio.SetActive(true)
		} else {
			radio.SetGroup(group)
		}
		radio.ConnectToggled(func() {
			if radio.Active() {
				picked = b.Path
			}
		})
		list.Append(radio)
	}
	d.SetExtraChild(list)
	d.AddResponse("cancel", "Cancel")
	d.AddResponse("restore", "Restore and Close")
	d.SetResponseAppearance("restore", adw.ResponseDestructive)
	d.SetDefaultResponse("cancel")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		if response != "restore" {
			return
		}
		a.pendingRestore = picked
		a.adw.Quit()
	})
	d.Present(a.win)
}

// finishRestore puts the chosen backup back, once the library is closed.
func (a *App) finishRestore() {
	if a.pendingRestore == "" {
		return
	}
	kept, err := store.RestoreBackup(store.DefaultDBPath(), a.pendingRestore, time.Now())
	if err != nil {
		log.Printf("astral: restoring %s: %v", a.pendingRestore, err)
		_ = os.WriteFile(restoredNote(), []byte("failed\n"+err.Error()), 0o600)
		return
	}
	_ = os.WriteFile(restoredNote(), []byte(filepath.Base(a.pendingRestore)+"\n"+kept), 0o600)
}

// announceRestore says, on the launch after one, that a backup was put back
// and where the library it replaced was kept.
func (a *App) announceRestore() {
	raw, err := os.ReadFile(restoredNote())
	if err != nil {
		return
	}
	_ = os.Remove(restoredNote())
	lines := strings.SplitN(string(raw), "\n", 2)
	if lines[0] == "failed" {
		reason := ""
		if len(lines) > 1 {
			reason = lines[1]
		}
		a.toast("The backup could not be restored, so your library is as it was: " + reason)
		return
	}
	a.toast("Restored " + lines[0] + ". Your previous library is kept beside it.")
}

func backupDay(t time.Time) string {
	today := time.Now()
	switch {
	case t.Format("2006-01-02") == today.Format("2006-01-02"):
		return "Today"
	case t.Format("2006-01-02") == today.AddDate(0, 0, -1).Format("2006-01-02"):
		return "Yesterday"
	}
	return t.Format("Monday 2 January")
}

func sizeOf(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", n>>10)
}
