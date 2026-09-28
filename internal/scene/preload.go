package scene

import (
	"context"
	"strings"
	"sync"
	"time"

	"astral/internal/gpu"
	"astral/internal/ollama"
)

// Loading the model while someone is still typing.
//
// Astral no longer holds a model in memory for half an hour after every reply:
// the server's own keep-alive applies, which on a machine set up to avoid
// crowding video memory is a few minutes. So after a pause the model has often
// been unloaded, and the next message would wait for it to load back before
// the first word. Starting the load at the first keystroke puts that wait
// behind the typing instead of in front of the reply.

// preloadEvery bounds how often a keystroke may ask. The first one after a
// pause is the one that matters; the rest of the message would only be asking
// again for a model that is already on its way.
const preloadEvery = 45 * time.Second

var (
	preloadMu   sync.Mutex
	preloadLast = map[string]time.Time{}
)

// PreloadForTyping loads model if it is not loaded already, and does nothing
// when that would not be safe or would be wasted. Call it from a goroutine.
//
// Only when it fits. The reply would load the model anyway, but a preload
// happens without anyone pressing anything, and adding a large model to a
// card with something else on it is how the desktop freezes. If it does not
// fit, the send will load it as it always has, and releasing the previous
// model first (UseForReplies) still happens there.
func PreloadForTyping(client *ollama.Client, model string) {
	model = strings.TrimSpace(model)
	if client == nil || model == "" {
		return
	}
	preloadMu.Lock()
	if time.Since(preloadLast[model]) < preloadEvery {
		preloadMu.Unlock()
		return
	}
	preloadLast[model] = time.Now()
	preloadMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	loaded, err := client.Running(ctx)
	if err != nil {
		return
	}
	if _, ok := ollama.FindLoaded(loaded, model); ok {
		return // already there, which is the common case mid-conversation
	}
	size := int64(0)
	if models, err := client.Models(ctx); err == nil {
		for _, m := range models {
			if strings.TrimSuffix(m.Name, ":latest") == strings.TrimSuffix(model, ":latest") {
				size = m.Size
				break
			}
		}
	}
	if size <= 0 || !gpu.Fits(uint64(float64(size)*contextHeadroom)) {
		return
	}
	_ = client.Preload(ctx, model)
}
