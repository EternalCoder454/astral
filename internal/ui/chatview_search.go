package ui

import (
	"strings"

	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/websearch"
)

// Web search, for the conversations that are not roleplay.
//
// A local model's knowledge stops where its weights stop, which in a scene does
// not matter and everywhere else matters constantly. The tools are offered to
// every conversation that is not a scene (General Chat, the designers and the
// Prompt Optimizer), and never to a scene: a roleplay does not want facts from
// outside it, and cannot survive the model breaking off to report what it
// found on the internet.

// searcher builds the tool loop for this turn: search when it is switched on,
// saving to the knowledge base, and for the Prompt Optimizer reading Astral's
// prompts. Nil for a scene, which is given no tools. See scene.Runner.
func (c *ChatView) searcher(model string, opts ollama.Options, think *bool) *websearch.Runner {
	r := scene.Runner(c.client, c.cfg, c.store, c.chat.Kind, model, opts, think)
	if r == nil {
		return nil
	}
	// On the worker goroutine: it only marks the buffer, and the flush on the
	// UI thread clears the row.
	r.OnDiscard = func() {
		c.pendMu.Lock()
		c.pendText.Reset()
		c.pendDiscard = true
		c.pendMu.Unlock()
	}
	// A search is seconds with nothing arriving. Saying what is being looked
	// up is the difference between waiting and wondering whether it has
	// stalled.
	r.OnRound = func(round websearch.Round) {
		status := "Searching for \"" + round.Query + "\"…"
		switch {
		case round.Note != "":
			status = round.Note
		case round.Opened != "":
			status = "Reading " + strings.TrimPrefix(strings.TrimPrefix(round.Opened, "https://"), "http://") + "…"
		}
		c.pendMu.Lock()
		c.pendStatus = status
		c.pendMu.Unlock()
	}
	return r
}

// SearchNotes is what this turn looked up, for the fold above the reply.
func (c *ChatView) SearchNotes() string { return c.searchNotes }
