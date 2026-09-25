package app

import (
	"runtime"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Installing an update.
//
// Astral is built from source, so an update is a fetch and a rebuild rather
// than a downloaded binary. That is slower than it would be with a release
// artifact, and it has one large advantage: what you end up running was built
// against the GTK and libadwaita on this machine, which is the thing that
// actually breaks when a binary is carried between distributions.
//
// The clone it builds from is the updater's own, under the user's data
// directory. It is never a developer checkout, so an update cannot silently
// throw away work in progress.

// repoURL is where updates come from.
const repoURL = projectURL + ".git"

// startUpdate runs the update behind a dialog that reports what is happening.
func (a *App) startUpdate() {
	if runtime.GOOS != "linux" {
		a.toast("Automatic updates are only set up for Linux. " +
			"Download the latest version from " + projectURL)
		return
	}
	d := adw.NewAlertDialog("Updating Astral", "")
	status := gtk.NewLabel("Fetching and building the new version.\nThis takes a minute or two.")
	status.SetXAlign(0)
	status.SetWrap(true)
	status.SetMaxWidthChars(notesWidthChars)
	d.SetExtraChild(status)
	d.AddResponse("close", "Close")
	d.SetCloseResponse("close")
	d.Present(a.win)

	a.installUpdate(a.cfg.UpdateChannel, func(text string, done bool) {
		status.SetText(text)
		if done {
			d.SetResponseEnabled("close", true)
		}
	})
}
