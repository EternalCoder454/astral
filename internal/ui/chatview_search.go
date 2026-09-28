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
// not matter and everywhere else matters constantly. The tool is offered to the
// general chat and to the three designers, and never to a scene: a roleplay does
// not want facts from outside it, and cannot survive the model breaking off to
// report what it found on the internet.

// searcher builds the runner for this turn, or nil when this conversation cannot
// search or search is switched off.
func (c *ChatView) searcher(model string, opts ollama.Options, think *bool) *websearch.Runner {
	if !scene.Searchable(c.cfg) || !scene.CanSearch(c.chat.Kind) {
		return nil
	}
	return &websearch.Runner{
		Client:   c.client,
		Provider: scene.SearchProvider(c.cfg),
		Fetcher:  scene.Fetcher(),
		Model:    model,
		Options:  opts,
		Think:    think,
		Results:  c.cfg.SearchResults,
		// On the worker goroutine: it only marks the buffer, and the flush on
		// the UI thread clears the row.
		OnDiscard: func() {
			c.pendMu.Lock()
			c.pendText.Reset()
			c.pendDiscard = true
			c.pendMu.Unlock()
		},
		// A search is seconds with nothing arriving. Saying what is being
		// looked up is the difference between waiting and wondering whether
		// it has stalled.
		OnRound: func(r websearch.Round) {
			status := "Searching for \"" + r.Query + "\"…"
			if r.Opened != "" {
				status = "Reading " + strings.TrimPrefix(strings.TrimPrefix(r.Opened, "https://"), "http://") + "…"
			}
			c.pendMu.Lock()
			c.pendStatus = status
			c.pendMu.Unlock()
		},
	}
}

// SearchNotes is what this turn looked up, for the fold above the reply.
func (c *ChatView) SearchNotes() string { return c.searchNotes }
