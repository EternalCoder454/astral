package scene

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"astral/internal/ollama"
	"astral/internal/promptopt"
	"astral/internal/prompts"
	"astral/internal/store"
	"astral/internal/websearch"
)

// The tools a conversation is given, the same on the window and the phone.
//
// Every conversation that is not a scene can search the web, read the pages it
// finds, and keep what it finds worth keeping in the knowledge base. A scene gets
// none of them: a character with a search engine is a character who stops the
// story to report what it found.

var promptSaveGuidance = prompts.Register(prompts.Prompt{
	ID: "chat.save", Name: "Knowledge Saving Guidance", Group: "Conversation",
	About: "Added to the system prompt of every conversation that is not a scene, to teach the model when " +
		"to save what it finds to the knowledge base.",
	Keep:    "The tool name " + websearch.SaveToolName + " is what the model calls, so it must stay.",
	Default: websearch.SaveGuidance,
})

// saveTool saves to the knowledge base.
var saveTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name: websearch.SaveToolName,
		Description: "Save something worth keeping to the person's knowledge base, where later conversations " +
			"will find it: a checked fact, a reference, figures, how something works, what a good source said. " +
			"Not opinions, guesses, or anything only true inside this conversation.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "title": {"type": "string", "description": "What it is about, in a few words"},
    "content": {"type": "string", "description": "The note itself, specific and complete enough to make sense on its own later"},
    "source": {"type": "string", "description": "The address it came from, when it came from a page"},
    "tags": {"type": "array", "items": {"type": "string"}, "description": "One to four short labels"}
  },
  "required": ["title", "content"]
}`),
	},
}

// maxSavedNote bounds one saved note. A model that pastes a whole page into a
// note has made a copy of the page, not a note, and a page can be kept whole
// with Keep Reading if that is what is wanted.
const maxSavedNote = 12000

// KnowledgeSaver is the tool that saves a note to the knowledge base.
func KnowledgeSaver(st *store.Store) websearch.Extra {
	parse := func(args json.RawMessage) (title, content, source string, tags []string) {
		var a struct {
			Title   string   `json:"title"`
			Content string   `json:"content"`
			Source  string   `json:"source"`
			Tags    []string `json:"tags"`
		}
		_ = json.Unmarshal(args, &a)
		return strings.TrimSpace(a.Title), strings.TrimSpace(a.Content), strings.TrimSpace(a.Source), a.Tags
	}
	return websearch.Extra{
		Tool: saveTool,
		Answer: func(ctx context.Context, args json.RawMessage) string {
			title, content, source, tags := parse(args)
			if title == "" || content == "" {
				return "Nothing was saved: a note needs a title and its content."
			}
			if r := []rune(content); len(r) > maxSavedNote {
				content = string(r[:maxSavedNote])
			}
			if len(tags) > 4 {
				tags = tags[:4]
			}
			if _, err := st.SaveKnowledge(store.KnowledgeEntry{
				Title: title, Body: content, Source: source, Origin: store.OriginChat,
				Tags: append(tags, "saved"),
			}); err != nil {
				return "It could not be saved: " + err.Error()
			}
			return fmt.Sprintf("Saved %q to the knowledge base.", title)
		},
		Note: func(args json.RawMessage) string {
			if title, _, _, _ := parse(args); title != "" {
				return fmt.Sprintf("Saved %q to Knowledge", title)
			}
			return ""
		},
	}
}

// Extras are the tools a conversation of this kind is given beside search.
// Saving goes with searching: what is worth keeping is what was found, and
// offered without search, a designer took it as a way to keep the character
// it was designing, and said it had when it had not. Looking things up in the
// knowledge base does not: it is offered whenever there is something there.
func Extras(st *store.Store, client *ollama.Client, cfg store.Config, kind string) []websearch.Extra {
	if !CanSearch(kind) {
		return nil
	}
	var out []websearch.Extra
	if HasKnowledge(st) {
		out = append(out, KnowledgeSearcher(st, client, cfg))
	}
	if st != nil && Searchable(cfg) {
		out = append(out, KnowledgeSaver(st))
	}
	if kind == store.KindPromptOptimizer {
		out = append(out, promptopt.ReadExtra())
	}
	return out
}

// Runner is the tool loop for a conversation, or nil for a scene, which is
// given no tools. Search is offered only while it is switched on; the other
// tools are offered either way. The caller fills in the callbacks.
func Runner(client *ollama.Client, cfg store.Config, st *store.Store, kind, model string,
	opts ollama.Options, think *bool) *websearch.Runner {
	if !CanSearch(kind) {
		return nil
	}
	r := &websearch.Runner{
		Client:  client,
		Model:   model,
		Options: opts,
		Think:   think,
		Results: cfg.SearchResults,
		Extras:  Extras(st, client, cfg, kind),
	}
	if Searchable(cfg) {
		r.Provider = SearchProvider(cfg)
		r.Fetcher = Fetcher()
	}
	return r
}
