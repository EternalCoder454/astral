package scene

import (
	"context"
	"log"
	"net/url"
	"strings"
	"time"

	"astral/internal/knowledge"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/websearch"
)

// embedModel remembers which embedding model is installed, shared by both
// clients so the server is asked once rather than once each.
var embedModel knowledge.Model

// EmbedModel is the embedding model to use, or "" for words alone.
func EmbedModel(ctx context.Context, client *ollama.Client, cfg store.Config) string {
	return embedModel.Resolve(ctx, client, cfg.EmbeddingModel)
}

// UsesKnowledge reports whether a conversation of this kind draws on the
// knowledge base: the same ones that can search. A scene has its world and its
// own history, and a note about a Python library has no business in a tavern.
func UsesKnowledge(kind string) bool { return CanSearch(kind) }

// WithKnowledge adds what the knowledge base holds about this turn to the end
// of a request, and returns it unchanged when there is nothing relevant.
//
// At the end, after the conversation, for the same reason lore goes there: it
// changes with every message, and anything that changes has to come after the
// part that does not, or the server re-reads the whole conversation each turn
// instead of only what is new.
//
// Call it off the UI thread: it may ask the embedding model for a vector.
func WithKnowledge(ctx context.Context, st *store.Store, client *ollama.Client, cfg store.Config,
	kind string, msgs, hist []ollama.Message) []ollama.Message {
	if st == nil || !UsesKnowledge(kind) {
		return msgs
	}
	query := knowledge.QueryFrom(hist)
	if designing(kind) && !lookupOff {
		query = designQuery(hist)
	}
	var block string
	if query != "" {
		block = knowledge.Render(retrieve(ctx, st, client, cfg, query), time.Now())
	}
	if titles := titlesNote(st, cfg); titles != "" {
		block = strings.TrimSpace(block + "\n\n" + titles)
	}
	if block == "" {
		return msgs
	}
	note := ollama.Message{Role: ollama.RoleSystem, Content: block}
	out := make([]ollama.Message, 0, len(msgs)+1)
	// Before a reply that has been started for the model, not after it: an
	// unfinished assistant turn has to be the last thing in the request or it
	// is no longer something to continue.
	if n := len(msgs); n > 0 && msgs[n-1].Role == ollama.RoleAssistant {
		out = append(out, msgs[:n-1]...)
		out = append(out, note)
		return append(out, msgs[n-1])
	}
	out = append(out, msgs...)
	return append(out, note)
}

// designing reports whether a conversation is designing something: a
// character, a world, a style or a persona.
func designing(kind string) bool {
	switch kind {
	case store.KindDesigner, store.KindWorldDesigner, store.KindStyleDesigner, store.KindPersonaDesigner:
		return true
	}
	return false
}

// designQuery is what a design conversation is about: the first thing the
// person said, which is where the subject is usually named, then the rest,
// newest first, up to maxBuildQuery characters.
//
// Not only the last two messages, as for a conversation that moves from
// subject to subject. A design is about one thing from start to finish, and
// the town a character comes from is named once, in the first message: by
// the fourth, "give her a secret" matched nothing, and the notes on the town
// were no longer in front of the designer at all. The first message is kept
// whatever follows, so a long paste late in the design cannot push it out.
func designQuery(hist []ollama.Message) string {
	var said []string
	for _, m := range hist {
		if m.Role == ollama.RoleUser && strings.TrimSpace(m.Content) != "" {
			said = append(said, m.Content)
		}
	}
	if len(said) == 0 {
		return ""
	}
	clip := func(s string, n int) string {
		if r := []rune(s); len(r) > n {
			return string(r[:n])
		}
		return s
	}
	var b strings.Builder
	b.WriteString(clip(said[0], maxBuildQuery/2))
	for i := len(said) - 1; i > 0 && b.Len() < maxBuildQuery; i-- {
		b.WriteString("\n")
		b.WriteString(said[i])
	}
	return strings.TrimSpace(clip(b.String(), maxBuildQuery))
}

// KeepPage saves a page a search opened, when the settings say to keep them.
//
// Saved whole, under its address, so reading the same page again replaces it
// rather than piling up copies. The model's answer is not what is kept: the
// answer may be wrong, and the page is what it was drawn from.
func KeepPage(st *store.Store, cfg store.Config, round websearch.Round) {
	if st == nil || !cfg.KeepReading || round.Page == nil || strings.TrimSpace(round.Page.Text) == "" {
		return
	}
	p := round.Page
	title := strings.TrimSpace(p.Title)
	if title == "" {
		if u, err := url.Parse(p.URL); err == nil {
			title = u.Host + u.Path
		} else {
			title = p.URL
		}
	}
	if _, err := st.SaveKnowledgeBySource(store.KnowledgeEntry{
		Title:  title,
		Body:   p.Text,
		Source: p.URL,
		Origin: store.OriginWeb,
		Tags:   []string{"web"},
	}); err != nil {
		log.Printf("astral: keeping %s: %v", p.URL, err)
	}
}
