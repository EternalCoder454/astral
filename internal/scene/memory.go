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
	keptPins, kept := recall(st, ch, hist, budget)
	write := func(b *strings.Builder, ms []store.Moment) {
		for _, m := range ms {
			b.WriteString(speakerOf(m, nameOf, charName, userName))
			b.WriteString(": ")
			b.WriteString(m.Content)
			b.WriteString("\n\n")
		}
	}
	var b strings.Builder
	if len(keptPins) > 0 {
		// Labelled, and only when there are pins, so a scene without any
		// sends exactly the block it always has.
		b.WriteString("Pinned, to be kept in mind always:\n")
		write(&b, keptPins)
		if len(kept) > 0 {
			b.WriteString("Recalled because of what is happening now:\n")
		}
	}
	write(&b, kept)
	return strings.TrimSpace(b.String())
}

// speakerOf is who said a moment, by name.
func speakerOf(m store.Moment, nameOf func(int64) string, charName, userName string) string {
	if m.Role != ollama.RoleAssistant {
		return userName
	}
	if nameOf != nil && m.CharacterID != 0 {
		if n := nameOf(m.CharacterID); n != "" {
			return n
		}
	}
	return charName
}

// recall chooses the moments Memory sends: the pinned ones that fit, and
// then the ones the latest exchange brings to mind, each list in the order
// they happened. Both are cut to length.
func recall(st *store.Store, ch store.Chat, hist []ollama.Message, budget int) (keptPins, kept []store.Moment) {
	if st == nil || ch.ID == 0 || ch.SummaryUpto <= 0 || budget <= 0 {
		return nil, nil
	}
	// Pinned turns first: you asked for them to be kept, and they take the
	// room before anything a search happened to find. Newest first while
	// choosing, so a scene with more pins than room keeps the recent ones.
	pins, err := st.Pinned(ch.ID, ch.SummaryUpto)
	if err != nil {
		log.Printf("astral: reading pinned moments in chat %d: %v", ch.ID, err)
	}
	used := 0
	pinned := map[int64]bool{}
	for i := len(pins) - 1; i >= 0; i-- {
		m := pins[i]
		m.Content = excerpt(m.Content, pinnedChars)
		cost := len(m.Content) + 24
		if used+cost > budget {
			continue
		}
		used += cost
		pinned[m.ID] = true
		keptPins = append(keptPins, m)
	}
	sort.Slice(keptPins, func(i, j int) bool { return keptPins[i].ID < keptPins[j].ID })

	var query []string
	for i := len(hist) - 1; i >= 0 && len(query) < 2; i-- {
		if c := strings.TrimSpace(hist[i].Content); c != "" && hist[i].Role != ollama.RoleSystem {
			query = append(query, c)
		}
	}
	var moments []store.Moment
	if len(query) > 0 {
		text := strings.Join(query, "\n")
		moments, err = st.Moments(ch.ID, ch.SummaryUpto, text, 6)
		if err != nil {
			log.Printf("astral: recalling earlier moments in chat %d: %v", ch.ID, err)
		}
		// A moment that says the same thing in other words shares nothing with
		// the query for the search above to match. With an embedding model
		// installed, what it is about is searched too and the two are merged.
		if meant := byMeaning(st, ch); len(meant) > 0 {
			moments = fuseMoments(moments, meant)
		}
	}

	// Best first while choosing, so the budget goes to the most relevant;
	// then in the order they happened, so the block reads as a sequence.
	for _, m := range moments {
		if pinned[m.ID] {
			continue
		}
		m.Content = excerpt(m.Content, momentChars)
		cost := len(m.Content) + 24
		if used+cost > budget {
			continue
		}
		used += cost
		kept = append(kept, m)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	return keptPins, kept
}

// pinnedChars bounds one pinned moment. More than a recalled one gets: you
// chose it, and cutting it short would drop the part you pinned it for.
const pinnedChars = 1200

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
