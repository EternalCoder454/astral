package ui

import (
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
		Provider: websearch.NewSearXNG(c.cfg.SearXNGURL),
		Model:    model,
		Options:  opts,
		Think:    think,
		Results:  c.cfg.SearchResults,
	}
}

// SearchNotes is what this turn looked up, for the fold above the reply.
func (c *ChatView) SearchNotes() string { return c.searchNotes }
