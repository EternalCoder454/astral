package app

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/store"
	"astral/internal/update"
)

// Checking for a new version on launch.
//
// The check runs once, on a background thread, after the window is on screen.
// It must never be something you wait for, and a machine that is offline has
// to behave exactly like one that is up to date: silently.
//
// It is a plain read of a text file from the project's repository. Nothing
// about this machine, your characters or your scenes is sent, and the whole
// thing can be switched off in Settings.

// updateCheckDelay gives the window a moment to settle first, so a launch is
// never competing with a network request.
const updateCheckDelay = 1500

// maybeCheckForUpdate starts the launch-time check unless it has been switched
// off, or this is a measurement run.
func (a *App) maybeCheckForUpdate() {
	if !a.cfg.CheckUpdates || os.Getenv("ASTRAL_NO_UPDATE_CHECK") != "" {
		return
	}
	// A local notes file can be pointed at to exercise the whole path without
	// publishing a release to try it against.
	notesURL := os.Getenv("ASTRAL_UPDATE_NOTES_URL")
	// Screenshot and measurement runs stay off the network, unless the run is
	// here to test this path, which pointing it at notes of its own asks for.
	if devRun() && notesURL == "" {
		return
	}
	checker := update.New()
	if notesURL != "" {
		checker.NotesURL = notesURL
	}
	branch := a.cfg.UpdateChannel
	coreglib.TimeoutAdd(updateCheckDelay, func() bool {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			rel, err := checker.Check(ctx, branch, version)
			coreglib.IdleAdd(func() bool {
				switch {
				case err != nil:
					// Offline, or GitHub is having a day. Not worth a dialog.
					log.Printf("astral: update check: %v", err)
				case rel != nil:
					a.showUpdateFound(rel)
				}
				return false
			})
		}()
		return false
	})
}

// checkForUpdateNow is the manual check from Settings. Unlike the launch-time
// one it says so when there is nothing to report, because someone who pressed
// a button is owed an answer either way.
func (a *App) checkForUpdateNow() {
	a.toast("Checking for updates…")
	checker := update.New()
	if u := os.Getenv("ASTRAL_UPDATE_NOTES_URL"); u != "" {
		checker.NotesURL = u
	}
	branch := a.cfg.UpdateChannel
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rel, err := checker.Check(ctx, branch, version)
		coreglib.IdleAdd(func() bool {
			switch {
			case err != nil:
				a.toast("Could not check for updates: " + err.Error())
			case rel == nil:
				a.toast("Astral " + version + " is the latest on the " + branch + " channel.")
			default:
				a.showUpdateFound(rel)
			}
			return false
		})
	}()
}

// showUpdateFound presents what the new version brings, and offers to install
// it now or leave it.
func (a *App) showUpdateFound(rel *update.Release) {
	if a.win == nil {
		return
	}
	d := adw.NewAlertDialog("Astral "+rel.Version+" is available", "")
	d.SetExtraChild(updateNotes(rel))
	d.AddResponse("later", "Later")
	d.AddResponse("now", "Update now")
	d.SetResponseAppearance("now", adw.ResponseSuggested)
	d.SetDefaultResponse("now")
	d.SetCloseResponse("later")
	d.ConnectResponse(func(response string) {
		if response == "now" {
			a.startUpdate()
		}
	})
	d.Present(a.win)
}

// notesWidthChars is the widest a release note line may run before it wraps.
const notesWidthChars = 52

// updateNotes renders the release notes as the dialog's body.
func updateNotes(rel *update.Release) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.SetMarginTop(4)
	for _, note := range rel.Notes {
		l := gtk.NewLabel("• " + note)
		l.SetXAlign(0)
		l.SetWrap(true)
		l.SetMaxWidthChars(notesWidthChars)
		box.Append(l)
	}
	if len(rel.Notes) == 0 {
		l := gtk.NewLabel("No notes were published for this version.")
		l.SetXAlign(0)
		l.AddCSSClass("dim-label")
		box.Append(l)
	}
	return box
}

// channelLabel names a channel for the interface.
func channelLabel(channel string) string {
	if channel == store.ChannelBeta {
		return "Beta"
	}
	return "Release"
}
