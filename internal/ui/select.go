package ui

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Copying part of a reply.
//
// A bubble's text is a selectable GtkLabel, which in GTK4 means it has to be
// able to take focus: a label holds a selection only while it is focused, so a
// label with can-focus off highlights nothing and there is no way to drag over
// a line and copy it.
//
// Letting the labels take focus costs one thing. Enter-to-send and every
// keystroke are handled on the composer's own key controller, so with the
// cursor parked in a bubble, typing went nowhere: the character was swallowed
// by a label that has no notion of input. Which is presumably why can-focus was
// turned off in the first place.
//
// So typing anywhere in the transcript is sent to the composer instead. Keys
// that mean something to a selection are left alone, which is the whole list
// below and the reason this is a function rather than four lines inline.

// typingGoesToComposer sends ordinary keystrokes from the transcript to the
// composer, leaving selection and shortcut keys where they are.
//
// It runs in the capture phase on the scroller, so it sees a key before the
// focused label does, and it is one controller for the whole transcript rather
// than one per row: a long chat is hundreds of rows, and the last thing they
// need is another per-row object.
func typingGoesToComposer(scroll *gtk.ScrolledWindow, composer *gtk.TextView) {
	key := gtk.NewEventControllerKey()
	key.SetPropagationPhase(gtk.PhaseCapture)
	key.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
		if composer == nil || !redirectedToComposer(keyval, state) {
			return false
		}
		// A focused button in a row footer keeps its keyboard activation: space
		// and Enter on Copy or Edit must press the button, not type into the
		// composer. Asking what has focus is cheaper than the alternative, which
		// is a focus controller on every widget in every row.
		if root := scroll.Root(); root != nil {
			if _, onButton := root.Focus().(*gtk.Button); onButton {
				return false
			}
		}
		composer.GrabFocus()
		return key.Forward(composer)
	})
	scroll.AddController(key)
}

// redirectedToComposer reports whether a key press belongs in the message box
// rather than in the transcript.
func redirectedToComposer(keyval uint, state gdk.ModifierType) bool {
	// Anything held down with Ctrl, Alt or Super is a command, not typing, and
	// that covers the two that matter most here: Ctrl+C copies the selection and
	// Ctrl+A selects the whole message.
	if state&(gdk.ControlMask|gdk.AltMask|gdk.SuperMask) != 0 {
		return false
	}
	switch keyval {
	// Moving and extending a selection, with Shift or without.
	case gdk.KEY_Left, gdk.KEY_Right, gdk.KEY_Up, gdk.KEY_Down,
		gdk.KEY_Home, gdk.KEY_End, gdk.KEY_Page_Up, gdk.KEY_Page_Down,
		gdk.KEY_Insert, gdk.KEY_Tab, gdk.KEY_ISO_Left_Tab, gdk.KEY_Escape,
		gdk.KEY_Menu:
		return false
	}
	// Enter sends, and a printable character types. Everything else, including
	// every modifier and function key, produces no text and is left alone: a
	// bare Shift press must not throw the cursor into the composer halfway
	// through a Shift+drag selection.
	if keyval == gdk.KEY_Return || keyval == gdk.KEY_KP_Enter {
		return true
	}
	r := gdk.KeyvalToUnicode(keyval)
	return r >= 0x20 && r != 0x7f
}
