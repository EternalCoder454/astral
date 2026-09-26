package app

import (
	"fmt"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/ollama"
	"astral/internal/serve"
	"astral/internal/store"
	"astral/internal/ui"
)

// Phone access: letting another device on this network use this machine's
// Astral.
//
// The arrangement is the honest one for local models. A 27B does not run on a
// phone and will not for a long time, so the phone is a screen and the PC is
// the computer. Nothing goes to anyone's server, there is no account anywhere,
// and with this switched off Astral is exactly what it was.

// startPhoneAccess brings the server up, if it is wanted and not already up.
func (a *App) startPhoneAccess() {
	if a.phone == nil {
		a.phone = serve.New(a.store,
			func() store.Config { return a.cfg },
			func() *ollama.Client { return a.client },
			a.applyConfigFromPhone,
			version)
	}
	if err := a.phone.Start(a.cfg.PhonePort); err != nil {
		a.toast("Could not open phone access: " + err.Error())
		a.cfg.PhoneAccess = false
		return
	}
}

// applyConfigFromPhone takes a settings change made on a phone and puts it
// where the window will see it.
//
// It runs on the server's goroutine, so the parts that touch widgets are
// handed back to the main thread. Everything the phone can change is something
// the window displays somewhere, and a model chip still naming the old model is
// the kind of small wrongness that makes a person distrust the whole feature.
func (a *App) applyConfigFromPhone(cfg store.Config) error {
	if err := store.SaveConfig(cfg); err != nil {
		return err
	}
	coreglib.IdleAdd(func() bool {
		a.cfg = cfg
		if a.chat != nil {
			a.chat.SetConfig(cfg)
		}
		if a.sidebar != nil {
			a.sidebar.SetProfile(cfg.PersonaName, cfg.PersonaDescription)
		}
		return false
	})
	return nil
}

// stopPhoneAccess closes it, and any pairing in progress with it.
func (a *App) stopPhoneAccess() {
	if a.phone != nil {
		a.phone.Stop()
	}
}

// applyPhoneAccess makes the server match the setting.
func (a *App) applyPhoneAccess() {
	if a.cfg.PhoneAccess {
		a.startPhoneAccess()
		return
	}
	a.stopPhoneAccess()
}

// buildPhonePage is the whole of the feature's interface: a switch, the
// address to type in, a pairing code while one is open, and the list of what
// has been let in.
func (a *App) buildPhonePage(f *settingsForm) *gtk.Box {
	page := settingsPage()
	outer, card := groupCard("Phone access")

	f.phone = gtk.NewCheckButton()
	f.phone.SetChild(wrappingLabel("Let another device on this network use this Astral"))
	f.phone.SetActive(a.cfg.PhoneAccess)
	card.Append(f.phone)

	hint := wrappingLabel("Your phone runs the screen. This machine runs the model, " +
		"and nothing leaves it.")
	hint.AddCSSClass("settings-hint")
	card.Append(hint)

	// The addresses, so there is something to type into the phone.
	addrs := gtk.NewLabel("")
	addrs.SetXAlign(0)
	addrs.SetWrap(true)
	addrs.SetSelectable(true)
	addrs.AddCSSClass("settings-hint")
	card.Append(addrs)

	code := gtk.NewLabel("")
	code.SetXAlign(0)
	code.SetSelectable(true)
	code.AddCSSClass("pair-code")
	code.SetVisible(false)
	card.Append(code)

	pairBtn := gtk.NewButtonWithLabel("Start pairing")
	pairBtn.SetHAlign(gtk.AlignStart)
	card.Append(pairBtn)

	devicesOuter, devicesCard := groupCard("Paired devices")
	page.Append(outer)
	page.Append(devicesOuter)

	var refresh func()
	refresh = func() {
		port, running := 0, false
		if a.phone != nil {
			port, running = a.phone.Running()
		}
		if !running {
			addrs.SetText("Not running.")
			pairBtn.SetSensitive(false)
			code.SetVisible(false)
		} else {
			list := serve.Addresses(port)
			if len(list) == 0 {
				addrs.SetText("Running, but this machine has no network address to reach it on.")
			} else {
				addrs.SetText("On your phone, open:\n" + strings.Join(list, "\n"))
			}
			pairBtn.SetSensitive(true)
			if c, left, open := a.phone.PairingOpen(); open {
				code.SetVisible(true)
				code.SetText(c + "   (" + fmt.Sprintf("%d", int(left.Minutes())+1) + " min left)")
				pairBtn.SetLabel("Stop pairing")
			} else {
				code.SetVisible(false)
				pairBtn.SetLabel("Start pairing")
			}
		}

		for {
			child := devicesCard.FirstChild()
			if child == nil {
				break
			}
			devicesCard.Remove(child)
		}
		devices, err := a.store.Devices()
		if err != nil {
			devicesCard.Append(wrappingLabel("Could not read the list: " + err.Error()))
			return
		}
		if len(devices) == 0 {
			none := wrappingLabel("Nothing is paired.")
			none.AddCSSClass("settings-hint")
			devicesCard.Append(none)
			return
		}
		for _, d := range devices {
			devicesCard.Append(a.deviceRow(d, refresh))
		}
	}

	pairBtn.ConnectClicked(func() {
		if a.phone == nil {
			return
		}
		if _, _, open := a.phone.PairingOpen(); open {
			a.phone.ClosePairing()
			refresh()
			return
		}
		if _, err := a.phone.OpenPairing(); err != nil {
			a.toast("Could not start pairing: " + err.Error())
			return
		}
		refresh()
		// The code carries a countdown, and it expires on its own, so the
		// panel has to keep up with it while it is on screen.
		coreglib.TimeoutAdd(20_000, func() bool {
			if a.phone == nil {
				return false
			}
			_, _, open := a.phone.PairingOpen()
			refresh()
			return open
		})
	})

	f.phone.ConnectToggled(func() {
		a.cfg.PhoneAccess = f.phone.Active()
		a.applyPhoneAccess()
		if err := store.SaveConfig(a.cfg); err != nil {
			a.toast("Could not save: " + err.Error())
		}
		refresh()
	})

	refresh()
	return page
}

// deviceRow is one paired device, and the way to revoke it.
func (a *App) deviceRow(d store.Device, done func()) *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 10)
	col := gtk.NewBox(gtk.OrientationVertical, 1)
	col.SetHExpand(true)

	name := gtk.NewLabel(d.Name)
	name.SetXAlign(0)
	name.AddCSSClass("launch-row-title")
	col.Append(name)

	seen := gtk.NewLabel("Last used " + agoText(d.LastSeen))
	seen.SetXAlign(0)
	seen.AddCSSClass("settings-hint")
	col.Append(seen)
	row.Append(col)

	remove := gtk.NewButtonFromIconName(ui.IconTrash)
	remove.SetTooltipText("Remove this device")
	remove.AddCSSClass("flat")
	remove.SetVAlign(gtk.AlignCenter)
	remove.ConnectClicked(func() {
		if err := a.store.DeleteDevice(d.ID); err != nil {
			a.toast("Could not remove it: " + err.Error())
			return
		}
		a.toast(d.Name + " can no longer connect.")
		done()
	})
	row.Append(remove)
	return row
}

// agoText is a rough age, which is all this needs to be.
func agoText(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
