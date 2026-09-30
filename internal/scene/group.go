package scene

import (
	"log"
	"strings"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// A scene with a cast goes through the same assembler as a two-hander, for the
// same reason: the window and the phone have to produce the same prompt for the
// same scene, or the characters answer differently depending on which screen you
// are looking at.

// BuildFor assembles the messages for one turn of a scene with any number of
// characters in it.
//
// A cast of one, or none, is an ordinary scene and goes through Build unchanged.
// That is not a special case bolted on: most scenes have one character, and they
// must keep producing exactly the prompt they produced before, down to the byte,
// or every existing conversation loses its cached prefix the first time it is
// reopened.
func BuildFor(st *store.Store, cfg store.Config, ch store.Chat, cast []chars.Character, hist []ollama.Message) []ollama.Message {
	return BuildTurn(st, cfg, ch, cast, hist, Turn{})
}

// Turn is how one turn is steered, on top of everything the scene already
// says. The zero value is an ordinary turn.
type Turn struct {
	// Note is what the person asked of a reply they are having written again.
	Note string
	// Speaker is who answers, in a group scene, when the person chose.
	Speaker string
	// Onward is a group turn with nothing new from the person: the cast
	// carry the scene on among themselves.
	Onward bool
	// Nudge is a turn the character starts after the person has been quiet
	// for a while, in a scene with one character. A group's is Onward.
	Nudge bool
}

// BuildTurn is BuildFor, steered.
func BuildTurn(st *store.Store, cfg store.Config, ch store.Chat, cast []chars.Character, hist []ollama.Message, t Turn) []ollama.Message {
	if len(cast) < 2 {
		var one chars.Character
		if len(cast) == 1 {
			one = cast[0]
		}
		return withNote(buildOne(st, cfg, ch, one, hist, t.Note, t.Nudge), ch, one, t.Note)
	}
	if t.Nudge {
		// A group has no one to write first to: the cast carry the scene on.
		t.Onward = true
	}
	switch ch.Kind {
	case store.KindDesigner, store.KindStyleDesigner, store.KindAssistant, store.KindPromptOptimizer, store.KindNovel:
		// These have no cast by construction. Answered here rather than left to
		// fall through, so a stray cast row on one of them cannot turn the
		// character designer into a roleplay.
		return withNote(Build(st, cfg, ch, chars.Character{}, hist), ch, chars.Character{}, t.Note)
	}

	p := Persona(cfg)
	// How the cast know each other, for the pairs both in the scene. Read here
	// rather than passed in, so the window and the phone cannot disagree about
	// whether a scene's people have met.
	rels := Relations(st, cast)
	sc := chars.Scene{
		Persona:          p,
		Recap:            ch.Summary,
		History:          hist,
		Relations:        rels,
		Budget:           GroupBudget(cfg, cast, p, rels),
		StyleChanged:     StyleChanged(cfg, ch, len(hist)),
		Direction:        ch.Note,
		NarrationDrifted: chars.NarrationDrifted(hist),
		// And the phrasing the recent replies keep coming back to, named in
		// the closing block so the next reply reaches for something else.
		// Measured on a sixteen-turn scene over two experiments, it took the
		// share of a reply's phrases already used in the last five replies
		// from 0.87 to 0.78 and from 0.93 to 0.68.
		Overused: chars.Overused(hist),
		RollCall: chars.RollCall(assistantTurns(hist), chars.CastNames(cast)),
		Speakers: speakers(cast, hist, t),
		Note:     t.Note,
		Onward:   t.Onward,
		Setting:  ch.Setting,
		State:    ch.State,
		Length:   ch.ReplyLength,
		Arrivals: arrivals(st, ch, cast),
	}
	sc.Lore = GroupLore(st, ch, cast, hist, sc.Budget.Lore)
	byID := make(map[int64]string, len(cast))
	for _, c := range cast {
		byID[c.ID] = c.Name
	}
	sc.Memory = Memory(st, ch, hist, func(id int64) string { return byID[id] },
		cast[0].Name, userNameOf(cfg), sc.Budget.Memory)
	return chars.BuildGroupMessages(cast, sc)
}

// speakers is who answers this turn: whoever the person chose, or else
// whoever ChooseSpeakers picks. A turn that carries on without the person is
// not answering anything they said, so an old message of theirs naming
// somebody does not decide it.
func speakers(cast []chars.Character, hist []ollama.Message, t Turn) []string {
	names := chars.CastNames(cast)
	if t.Speaker != "" {
		for _, n := range names {
			if strings.EqualFold(n, t.Speaker) {
				return []string{n}
			}
		}
	}
	said := lastUserTurn(hist)
	if t.Onward {
		said = ""
	}
	return chars.ChooseSpeakers(names, said, assistantTurns(hist))
}

// GroupBudget divides the context window for a cast.
//
// The system prompt is measured rather than estimated, and for a group that
// matters more than it does for one character: five cards' descriptions are
// thousands of characters that have to come out of the transcript rather than out
// of the window.
func GroupBudget(cfg store.Config, cast []chars.Character, p chars.Persona, rels []chars.Relation) chars.Budget {
	numCtx := cfg.NumCtx
	if numCtx <= 0 {
		numCtx = chars.DefaultNumCtx
	}
	// Room kept as well for saying who arrived partway through, which is in
	// the closing block of a group scene and nowhere else. Kept whether or
	// not anyone has, so the budget is the cast's alone and everything that
	// works it out agrees without reading when each member came: it is a few
	// hundred characters of a window.
	arrivals := chars.ArrivalsRoom(len(cast) - 1)
	return chars.Plan(numCtx, cfg.NumPredict, len(chars.BuildGroupSystem(cast, p, rels))+arrivals)
}

// arrivals are the members of a group scene's cast who came partway through,
// and what was said first after each came. See chars.ArrivalsBlock.
func arrivals(st *store.Store, ch store.Chat, cast []chars.Character) []chars.Arrival {
	if st == nil || ch.ID == 0 || len(cast) < 2 {
		return nil
	}
	joins, err := st.CastJoins(ch.ID)
	if err != nil || len(joins) == 0 {
		return nil
	}
	var out []chars.Arrival
	for _, c := range cast {
		after, ok := joins[c.ID]
		if !ok {
			continue
		}
		a := chars.Arrival{Name: c.Name}
		if m, ok := st.FirstMessageAfter(ch.ID, after); ok {
			if m.ID > ch.SummaryUpto {
				a.Since = chars.ArrivalSince(m.Content)
			} else {
				a.Recorded = true
			}
		}
		out = append(out, a)
	}
	return out
}

// Relations is how the members of a cast know each other, for the pairs where
// both are in the scene.
//
// A relation with somebody who is not in the room is noise, and a two-hander has
// no pairs at all, so this is empty for almost every conversation.
func Relations(st *store.Store, cast []chars.Character) []chars.Relation {
	if st == nil || len(cast) < 2 {
		return nil
	}
	ids := make([]int64, 0, len(cast))
	for _, c := range cast {
		if c.ID != 0 {
			ids = append(ids, c.ID)
		}
	}
	rels, err := st.RelationsAmong(ids)
	if err != nil {
		log.Printf("astral: reading relations for a scene: %v", err)
		return nil
	}
	return rels
}

// GroupLore is the world block for a scene with a cast: the setting, plus
// whichever entries the conversation is currently touching.
//
// The world comes from the first member that has one. A cast drawn from two
// worlds is a scene that has to happen in one of them, and the alternative,
// concatenating two lorebooks, spends the budget twice to describe a place that
// does not exist.
func GroupLore(st *store.Store, ch store.Chat, cast []chars.Character, hist []ollama.Message, budget int) string {
	return Lore(st, ch, groupHost(cast), hist, budget)
}

// groupHost is the member whose world a group scene is set in, standing for
// the whole cast: every member's own text goes in, so the lorebook is
// scanned against all of them. Zero when nobody has a world.
func groupHost(cast []chars.Character) chars.Character {
	var host chars.Character
	for _, c := range cast {
		if c.WorldID != 0 {
			host = c
			break
		}
	}
	if host.WorldID == 0 {
		return chars.Character{}
	}
	// Every member's own text is scanned for keywords, not just the host's: a
	// scene that has only just opened has almost no transcript, and the people
	// in it are the best clue to which parts of the world are about to matter.
	var b strings.Builder
	for _, c := range cast {
		b.WriteString(c.Description)
		b.WriteByte(' ')
		b.WriteString(c.Scenario)
		b.WriteByte(' ')
	}
	host.Description, host.Scenario = b.String(), ""
	return host
}

// History turns stored turns into the conversation the model sees, putting each
// speaker's name back on their words.
//
// The name is stored beside a message rather than inside it, so it has to go
// back on here. It is not optional: the transcript is the strongest instruction
// in the context, and a scene whose history arrives unlabelled is a scene
// teaching the model that replies carry no labels.
//
// Consecutive beats by the cast are merged into one assistant message. They were
// one reply when the model wrote them, several chat turns would imply the
// characters took turns being prompted, and a few model templates require the
// roles to alternate at all.
//
// Only beats, which is to say only messages that carry a speaker. Two adjacent
// replies in a scene with one character happen when you delete your own message
// between them, and merging those would change the prompt of a conversation that
// has nothing to do with groups.
func History(msgs []store.Message, nameOf func(int64) string) []ollama.Message {
	out, _ := HistoryWithIDs(msgs, nameOf)
	return out
}

// HistoryWithIDs is History, with the id of the last stored message each turn
// was made from. A turn is not always one message: a group's beats merge, and
// empty and hidden messages are left out. So a recap that covers the first n
// turns covers stored messages up to ids[n-1], and pairing turns with stored
// messages by position would put that boundary in the wrong place.
func HistoryWithIDs(msgs []store.Message, nameOf func(int64) string) ([]ollama.Message, []int64) {
	out := make([]ollama.Message, 0, len(msgs))
	ids := make([]int64, 0, len(msgs))
	merged := false // the previous message was a beat, so a beat can join it
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		// A hidden message is yours to read and not the model's.
		if content == "" || m.Hidden {
			continue
		}
		beat := m.Role == ollama.RoleAssistant && m.CharacterID != 0 && nameOf != nil
		if beat {
			content = chars.Label(nameOf(m.CharacterID), content)
		}
		if beat && merged && len(out) > 0 {
			out[len(out)-1].Content += "\n\n" + content
			ids[len(ids)-1] = m.ID
			continue
		}
		out = append(out, ollama.Message{Role: m.Role, Content: content})
		ids = append(ids, m.ID)
		merged = beat
	}
	return out, ids
}

// assistantTurns is the cast's replies from a history, oldest last, for the
// checks that look at how the scene has been going.
func assistantTurns(hist []ollama.Message) []string {
	out := make([]string, 0, 4)
	for _, m := range hist {
		if m.Role == ollama.RoleAssistant {
			out = append(out, m.Content)
		}
	}
	if len(out) > 4 {
		out = out[len(out)-4:]
	}
	return out
}

// lastUserTurn is the user's newest message, which a group turn answers.
func lastUserTurn(hist []ollama.Message) string {
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == ollama.RoleUser {
			return hist[i].Content
		}
	}
	return ""
}
