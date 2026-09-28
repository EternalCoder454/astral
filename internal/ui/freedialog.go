package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// FreeOnClose lets GTK free a dialog, and everything in it, once it has
// closed.
//
// A dialog's buttons nearly all close it, so their handlers hold the dialog,
// and GTK holds the buttons for as long as the dialog lives: a circle that
// passes through GTK, where Go's collector cannot see round it, so no dialog
// was ever freed. Measured, Characters kept 857 objects every time it was
// opened, and Settings 167. Letting go of the dialog's Go side once it has
// closed breaks the circle.
//
// Only for a dialog made fresh each time it is shown. Anything that touches
// it afterwards through the same variable finds nothing there: GTK logs a
// warning and does nothing, rather than crashing.
func FreeOnClose(d *adw.Dialog) {
	d.ConnectClosed(func() {
		// Later, not now: closed is emitted from inside the dialog's own
		// closing, and whatever else is listening for it runs after this.
		coreglib.TimeoutAdd(500, func() bool {
			coreglib.Destroy(d)
			return false
		})
	})
}
