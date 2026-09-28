package scene

import (
	"context"
	"strings"
	"sync"
	"time"

	"astral/internal/gpu"
	"astral/internal/ollama"
)

// Loading the model, and reading the scene, while someone is still typing.
//
// Astral no longer holds a model in memory for half an hour after every reply:
// the server's own keep-alive applies, which on a machine set up to avoid
// crowding video memory is a few minutes. So after a pause the model has often
// been unloaded, and the next message would wait for it to load back before
// the first word.
//
// It used to load the bare model at the first keystroke, and measured on
// SOMPOA that saved nothing: loaded without the scene's settings, it was
// loaded again the moment the message went, with the context size the scene
// asks for, and the first word still took 9.8 seconds. So the warm-up is the
// scene's own request instead, cut to a single token: the model loads with the
// right settings, and the scene's prompt is read and kept, so the reply only
// has to read what was typed. Measured on a forty-turn scene: first word 0.65
// seconds after the warm-up, against 9.6 without one.

// preloadEvery bounds how often a keystroke may ask. The first one after a
// pause is the one that matters; the rest of the message would only be asking
// again for a model that is already on its way.
const preloadEvery = 45 * time.Second

var (
	preloadMu   sync.Mutex
	preloadLast = map[string]time.Time{}
	// lastUsed is the chat each model last read, warmed or replied to, so a
	// warm-up for the chat it already holds is skipped.
	lastUsed = map[string]string{}
)

// NoteUsed records that model has just read chat's prompt, for a reply. A
// warm-up for the same chat then has nothing to do.
func NoteUsed(model, chat string) {
	preloadMu.Lock()
	lastUsed[strings.TrimSpace(model)] = chat
	preloadMu.Unlock()
}

// WarmForTyping gets the model and the scene ready while a message is being
// typed: it loads the model if need be, with the scene's settings, and reads
// the scene's prompt so the reply finds it cached. chat names the conversation
// and msgs is its prompt as it stands, before the message being typed. Call it
// from a goroutine.
//
// It does nothing when that would not be safe or would be wasted. Not when the
// model is loaded and already holds this chat. And not when the model is not
// loaded and would not fit: the reply would load it anyway, but a warm-up
// happens without anyone pressing anything, and adding a large model to a card
// with something else on it is how the desktop freezes. If it does not fit,
// the send will load it as it always has, releasing the previous model first
// (UseForReplies).
func WarmForTyping(client *ollama.Client, model, chat string, msgs []ollama.Message, opts ollama.Options) {
	model = strings.TrimSpace(model)
	if client == nil || model == "" || len(msgs) == 0 {
		return
	}
	key := model + "\x00" + chat
	preloadMu.Lock()
	if time.Since(preloadLast[key]) < preloadEvery {
		preloadMu.Unlock()
		return
	}
	preloadLast[key] = time.Now()
	holds := lastUsed[model] == chat
	preloadMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	loaded, err := client.Running(ctx)
	if err != nil {
		return
	}
	if _, ok := ollama.FindLoaded(loaded, model); ok {
		if holds {
			return // mid-conversation, the common case: already read
		}
	} else {
		size := int64(0)
		if models, err := client.Models(ctx); err == nil {
			for _, m := range models {
				if strings.TrimSuffix(m.Name, ":latest") == strings.TrimSuffix(model, ":latest") {
					size = m.Size
					break
				}
			}
		}
		if size <= 0 {
			return // not installed: the reply will say so
		}
		// What the reply would release first, released now: the model the
		// last reply was written with, when it is a different one.
		if len(loaded) > 0 {
			client.UseForReplies(ctx, model)
			if loaded, err = client.Running(ctx); err != nil {
				return
			}
		}
		// Room is asked about only when something else stays loaded. On an
		// empty card this is the load the reply would do anyway, and a large
		// model like SOMPOA never passes the check with the desktop's reserve
		// added, which is why loading at the first keystroke never happened
		// for it at all.
		if len(loaded) > 0 && !gpu.Fits(uint64(float64(size)*contextHeadroom)) {
			return
		}
	}
	opts.NumPredict = 1
	noThink := false
	if _, _, err := client.Chat(ctx, model, msgs, opts, &noThink, nil); err == nil {
		NoteUsed(model, chat)
	}
}
