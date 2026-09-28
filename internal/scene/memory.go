package scene

import (
	"log"
	"sort"
	"strings"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// momentChars bounds one recalled moment. A reply is often three paragraphs,
// and what makes it worth recalling is usually one of them; the rest of the
// room is better spent on a second moment.
const momentChars = 480

// Memory is the recalled-moments block for one turn of a scene: moments from
// the part of the scene the model can no longer see, that the latest exchange
// touches. Empty when the scene has not outgrown its window, and then the
// room goes to the transcript.
//
// The query is the newest message from the person and the reply before it,
// because between them they are what the scene is about right now. The
// person's message alone is often an action with no nouns in it.
func Memory(st *store.Store, ch store.Chat, hist []ollama.Message, nameOf func(int64) string,
	charName, userName string, budget int) string {
	if st == nil || ch.ID == 0 || ch.SummaryUpto <= 0 || budget <= 0 {
		return ""
	}
	var query []string
	for i := len(hist) - 1; i >= 0 && len(query) < 2; i-- {
		if c := strings.TrimSpace(hist[i].Content); c != "" && hist[i].Role != ollama.RoleSystem {
			query = append(query, c)
		}
	}
	if len(query) == 0 {
		return ""
	}
	moments, err := st.Moments(ch.ID, ch.SummaryUpto, strings.Join(query, "\n"), 6)
	if err != nil {
		log.Printf("astral: recalling earlier moments in chat %d: %v", ch.ID, err)
		return ""
	}

	// Best first while choosing, so the budget goes to the most relevant;
	// then in the order they happened, so the block reads as a sequence.
	var kept []store.Moment
	used := 0
	for _, m := range moments {
		m.Content = excerpt(m.Content, momentChars)
		cost := len(m.Content) + 24
		if used+cost > budget {
			continue
		}
		used += cost
		kept = append(kept, m)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })

	var b strings.Builder
	for _, m := range kept {
		who := userName
		if m.Role == ollama.RoleAssistant {
			who = charName
			if nameOf != nil && m.CharacterID != 0 {
				if n := nameOf(m.CharacterID); n != "" {
					who = n
				}
			}
		}
		b.WriteString(who)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

// excerpt cuts a moment to length at a sentence where it can.
func excerpt(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexAny(cut, ".!?\""); i > max/2 {
		return strings.TrimSpace(cut[:i+1]) + " …"
	}
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + " …"
}

// userNameOf is the name the persona goes by, or the default.
func userNameOf(cfg store.Config) string {
	if n := strings.TrimSpace(cfg.PersonaName); n != "" {
		return n
	}
	return chars.DefaultPersonaName
}
