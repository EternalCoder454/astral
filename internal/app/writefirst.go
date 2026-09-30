package app

import (
	"context"
	"errors"
	"log"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
)

// Writes First, on the desktop: a scene can ask its character to speak up
// after you have been quiet. The window is what runs it, so it only happens
// while Astral is open. See scene.WriteFirstDue for when a scene is due.

const (
	// writeFirstEvery is how often the window looks for a chat that is due.
	writeFirstEvery = 60 * time.Second
	// writeFirstRetry is how long a chat is left alone after a try that
	// failed, so a model that keeps failing is not asked every minute.
	writeFirstRetry = 30 * time.Minute
	// writeFirstTimeout bounds one message, as for a reply.
	writeFirstTimeout = 15 * time.Minute
)

// startWritesFirst begins the timer. Called once, when the window is up.
func (a *App) startWritesFirst() {
	coreglib.TimeoutAdd(uint(writeFirstEvery/time.Millisecond), func() bool {
		a.writeFirstTick()
		return true
	})
}

// writeFirstTick finds a chat that is due and has its character write, for at
// most one chat a tick, and never over a reply or a draft being written
// anywhere: in the window, or from a phone.
func (a *App) writeFirstTick() {
	if a.store == nil || a.client == nil || a.writingFirst {
		return
	}
	if a.chat != nil && a.chat.Busy() {
		return
	}
	if a.phone != nil && a.phone.Writing() {
		return
	}
	now := time.Now()
	var ch store.Chat
	for _, due := range scene.WriteFirstDueChats(a.store, now) {
		if until, held := a.writeFirstHeld[due.ID]; held && now.Before(until) {
			continue
		}
		// Not while you are typing into it: you are not quiet.
		if a.chat != nil && a.chat.Chat().ID == due.ID && a.chat.Composing() {
			continue
		}
		ch = due
		break
	}
	if ch.ID == 0 {
		return
	}
	a.writingFirst = true
	client, cfg, phone := a.client, a.cfg, a.phone
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), writeFirstTimeout)
		defer cancel()
		finish := func(w scene.Wrote, err error) {
			coreglib.IdleAdd(func() bool {
				a.writingFirst = false
				a.wroteFirst(ch, w, err)
				return false
			})
		}
		// Not when Ollama is not there, or the scene's model is not: nothing
		// is worth reporting, and the next minute looks again.
		model := ch.Model
		if model == "" {
			model = cfg.Model
		}
		models, err := client.Probe(ctx)
		if err != nil || !ollama.HasModel(models, model) {
			finish(scene.Wrote{}, errNoModel)
			return
		}
		if phone != nil {
			release, free := phone.Claim(ch.ID)
			if !free {
				finish(scene.Wrote{}, errNoModel)
				return
			}
			defer release()
		}
		w, err := scene.WriteFirst(ctx, client, a.store, cfg, ch)
		finish(w, err)
	}()
}

// errNoModel stands for a try that was not made, as opposed to one that failed.
var errNoModel = errors.New("no model to write with")

// wroteFirst is a character having written first, on the main thread: show
// it, and say so when nobody is looking at the window.
func (a *App) wroteFirst(ch store.Chat, w scene.Wrote, err error) {
	switch {
	case errors.Is(err, errNoModel), errors.Is(err, scene.ErrMovedOn):
		return
	case err != nil:
		log.Printf("astral: %s writing first in chat %d: %v", ch.Title, ch.ID, err)
		if a.writeFirstHeld == nil {
			a.writeFirstHeld = map[int64]time.Time{}
		}
		a.writeFirstHeld[ch.ID] = time.Now().Add(writeFirstRetry)
		return
	}
	a.refreshSidebar()
	// Shown in place when the chat is open and nothing is being written into
	// it; a chat mid-reply is left alone, and shows it when it is next opened.
	if a.chat != nil && a.chat.Chat().ID == ch.ID && !a.chat.Busy() {
		a.chat.ShowWritten(w.Messages)
	}
	title := w.Who
	if title == "" {
		title = ch.Title
	}
	a.notifyReply(title, w.Text)
}
