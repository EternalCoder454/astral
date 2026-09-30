package app

import (
	"fmt"
	"log"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"astral/internal/store"
)

// Deleting chats, one or many, with a way back.
//
// A deleted chat leaves the list at once and is deleted for good when the
// toast offering to undo it goes away, or when Astral closes, whichever comes
// first. That replaced a dialog asking whether you were sure, which is a
// question people answer yes to without reading, and which asked it once per
// chat: clearing out twenty old scenes was twenty dialogs.

// deleteChats takes chats off the list and offers to put them back.
func (a *App) deleteChats(ids []int64) {
	if len(ids) == 0 || a.store == nil {
		return
	}
	if a.deleting == nil {
		a.deleting = map[int64]bool{}
	}
	title := ""
	if ch, err := a.store.Chat(ids[0]); err == nil {
		title = ch.Title
	}
	open := a.chat.Chat().ID
	reopen := int64(0)
	for _, id := range ids {
		a.deleting[id] = true
		if id == open {
			reopen = id
		}
	}
	if reopen != 0 {
		a.chat.Clear()
		a.showWelcome()
	}
	a.refreshSidebar()

	msg := fmt.Sprintf("Deleted %d chats.", len(ids))
	if len(ids) == 1 {
		msg = "Deleted the chat."
		if title != "" {
			msg = fmt.Sprintf("Deleted “%s”.", title)
		}
	}
	undone := false
	t := adw.NewToast(msg)
	t.SetUseMarkup(false) // a chat's title or an error's text, not markup
	t.SetTimeout(8)
	t.SetButtonLabel("Undo")
	t.ConnectButtonClicked(func() {
		undone = true
		for _, id := range ids {
			delete(a.deleting, id)
		}
		a.refreshSidebar()
		if reopen != 0 && a.chat.Chat().ID == 0 {
			if err := a.openChat(reopen); err != nil {
				a.toast("Could not open that chat: " + err.Error())
			}
		}
	})
	t.ConnectDismissed(func() {
		if !undone {
			a.commitDeletes(ids)
		}
	})
	a.toasts.AddToast(t)
}

// commitDeletes deletes for good whichever of these chats are still waiting to
// be. A chat that was put back is not among them.
func (a *App) commitDeletes(ids []int64) {
	failed := 0
	for _, id := range ids {
		if !a.deleting[id] {
			continue
		}
		if err := a.store.DeleteChat(id); err != nil {
			log.Printf("astral: delete chat %d: %v", id, err)
			failed++
			continue
		}
		delete(a.deleting, id)
	}
	if failed > 0 {
		// Still off the list until Astral closes, and still there next time,
		// which is the safe way round to fail.
		a.toast(fmt.Sprintf("Could not delete %d of those chats.", failed))
	}
}

// commitAllDeletes finishes every delete still waiting on its toast, for when
// Astral is closing.
func (a *App) commitAllDeletes() {
	if len(a.deleting) == 0 || a.store == nil {
		return
	}
	ids := make([]int64, 0, len(a.deleting))
	for id := range a.deleting {
		ids = append(ids, id)
	}
	for _, id := range ids {
		if err := a.store.DeleteChat(id); err != nil {
			log.Printf("astral: delete chat %d: %v", id, err)
		}
	}
	clear(a.deleting)
}

// visibleChats is every chat less the ones waiting to be deleted.
func (a *App) visibleChats() ([]store.Chat, error) {
	chats, err := a.store.Chats()
	if err != nil || len(a.deleting) == 0 {
		return chats, err
	}
	kept := chats[:0]
	for _, ch := range chats {
		if !a.deleting[ch.ID] {
			kept = append(kept, ch)
		}
	}
	return kept, nil
}
