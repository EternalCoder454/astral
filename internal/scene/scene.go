// Package scene assembles the prompt for one turn of a conversation.
//
// It exists because there are now two clients. The desktop window and the
// server the phone talks to have to produce byte-identical prompts for the same
// scene, or the same character answers differently depending on which screen
// you are looking at, and the cache that makes a long scene fast is thrown away
// every time you switch. Assembling it in one place is the only way to be sure.
//
// Everything here is derived from the store and the settings, so it needs no
// window and can run on a background request.
package scene

import (
	"log"
	"strings"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/world"
)

// Persona is the user, as the model is told about them.
func Persona(cfg store.Config) chars.Persona {
	return chars.Persona{
		Name:        cfg.PersonaName,
		Description: cfg.PersonaDescription,
		// The rulebook, rendered. It occupies exactly the position the freeform
		// instruction block used to: stated once in the system message and
		// restated in the closing block, which is where a model obeys it.
		GlobalInstructions: cfg.RulesText(),
		Style:              cfg.Style(),
	}
}

// Budget divides the context window for this character and persona.
func Budget(cfg store.Config, ca chars.Character, p chars.Persona) chars.Budget {
	numCtx := cfg.NumCtx
	if numCtx <= 0 {
		numCtx = chars.DefaultNumCtx
	}
	return chars.Plan(numCtx, cfg.NumPredict, len(chars.BuildSystem(ca, p)))
}

// Narrator turns a world into something a scene can be played against, for a
// scene set in a place rather than with a person.
//
// It is never stored. The caller builds it fresh from the world each time, so
// editing the world changes the scenes already running in it, and it never
// appears in a cast because nobody wrote it.
func Narrator(w world.World) chars.Character {
	return chars.Character{
		Name:    w.Name,
		WorldID: w.ID,
		Description: "You are this place itself, and everyone in it.\n\n" +
			"There is no single character to play here. Narrate what {{user}} finds, " +
			"and play whoever they meet: give those people names, voices and reasons " +
			"of their own, and let them leave again. When nobody is speaking, the " +
			"place is: weather, noise, what is happening two streets away.\n\n" +
			"Never answer as {{user}} and never decide what they do.",
		Scenario: strings.TrimSpace(w.Description),
	}
}

// Lore is the world block for this turn: the setting, plus whichever entries
// the conversation is currently touching.
func Lore(st *store.Store, ca chars.Character, hist []ollama.Message, budget int) string {
	if st == nil || ca.WorldID == 0 || budget <= 0 {
		return ""
	}
	w, err := st.World(ca.WorldID)
	if err != nil {
		// A world deleted out from under a character is not an error worth
		// failing a turn for: the scene simply has no setting any more.
		return ""
	}
	entries, err := st.LoreEntries(ca.WorldID)
	if err != nil {
		log.Printf("astral: reading lore for world %d: %v", ca.WorldID, err)
		return ""
	}
	if len(entries) == 0 {
		// A world with no lore yet is still a setting: it has a name, a
		// description and its rules, and those are worth sending.
		return world.Render(w, nil)
	}
	turns := make([]string, 0, len(hist)+1)
	// The character's own description is scanned too. A scene that has only
	// just opened has almost no transcript, and without this the setting would
	// not appear until someone happened to name part of it out loud.
	turns = append(turns, ca.Description+" "+ca.Scenario)
	for _, m := range hist {
		turns = append(turns, m.Content)
	}
	return world.Render(w, world.Match(entries, world.RecentText(turns), budget))
}

// Build assembles the messages for one turn.
//
// hist is the conversation as it will be sent: everything since the recap, in
// order, with the new user message already on the end.
func Build(st *store.Store, cfg store.Config, ch store.Chat, ca chars.Character, hist []ollama.Message) []ollama.Message {
	system := func(content string) []ollama.Message {
		return append([]ollama.Message{{Role: ollama.RoleSystem, Content: content}}, hist...)
	}
	switch ch.Kind {
	// The designers are tools with a defined product, and the user's own rules
	// are not sent to them: a rule about how prose should read is right for a
	// scene and would break an interview whose answer has to parse.
	case store.KindDesigner:
		return system(chars.DesignerSystem)
	case store.KindStyleDesigner:
		return system(chars.StyleDesignerSystem)
	case store.KindWorldDesigner:
		return system(world.DesignerSystem)
	case store.KindAssistant:
		return Plain(cfg, ch, hist)
	}
	if ca.Name == "" {
		// A scene whose character was deleted. It is still readable and still
		// worth continuing, as a conversation rather than as a roleplay.
		return Plain(cfg, ch, hist)
	}

	p := Persona(cfg)
	sc := chars.Scene{
		Persona: p,
		Recap:   ch.Summary,
		History: hist,
		Budget:  Budget(cfg, ca, p),
		// The transcript is the strongest style signal in the context: by turn
		// twenty it holds twenty worked examples of how this scene sounds. If
		// the style has been changed since, saying nothing means the model
		// imitates what it can see and the new style changes almost nothing.
		StyleChanged: StyleChanged(cfg, ch, len(hist)),
		Direction:    ch.Note,
		// Likewise for markup. The first reply that drops the asterisks
		// becomes precedent for every reply after it, so the rule is restated
		// more firmly exactly while that is happening.
		NarrationDrifted: chars.NarrationDrifted(hist),
	}
	sc.Lore = Lore(st, ca, hist, sc.Budget.Lore)
	return chars.BuildMessages(ca, sc)
}

// Plain assembles a conversation that is not a roleplay.
//
// The recap is the point. Everything that keeps a long scene alive was written
// for a scene with a character in it, so a general chat had no recap at all and
// lost its own beginning the moment it outgrew the window. Now that it has one,
// this is what carries it: without this the turns it replaced would be dropped
// from the history and the record of them sent nowhere, which is worse than the
// bug it fixes.
func Plain(cfg store.Config, ch store.Chat, hist []ollama.Message) []ollama.Message {
	msgs := []ollama.Message{{
		Role:    ollama.RoleSystem,
		Content: chars.AssistantSystemFor(Persona(cfg)),
	}}
	if r := strings.TrimSpace(ch.Summary); r != "" {
		msgs = append(msgs, ollama.Message{
			Role: ollama.RoleSystem,
			Content: "Earlier in this conversation, in note form. Treat all of it as " +
				"already said and already settled, and do not go back over it.\n" + r,
		})
	}
	return append(msgs, hist...)
}

// PlainBudget divides the window for a conversation that is not a roleplay.
func PlainBudget(cfg store.Config) chars.Budget {
	numCtx := cfg.NumCtx
	if numCtx <= 0 {
		numCtx = chars.DefaultNumCtx
	}
	return chars.Plan(numCtx, cfg.NumPredict, len(chars.AssistantSystemFor(Persona(cfg))))
}

// StyleChanged reports whether the scene was written under a different style
// from the one now selected. Only once there is a transcript to be influenced
// by: on the first turn there is nothing for the model to imitate.
func StyleChanged(cfg store.Config, ch store.Chat, turns int) bool {
	if ch.ID == 0 || turns < 2 {
		return false
	}
	was := ch.StyleName
	return was != "" && was != cfg.Style().Name
}

// Options are the sampler settings for a reply.
//
// The reply limit is always sent, even when nothing is set. Unset, Ollama
// generates until it stops or fills the window, and the budget above reserves a
// fixed amount of room for the reply, so a reply that ignores that reservation
// puts the prompt back over the window and the oldest tokens, which are the
// framing, get dropped again.
func Options(cfg store.Config) ollama.Options {
	predict := cfg.NumPredict
	if predict <= 0 {
		predict = chars.DefaultReplyTokens
	}
	return ollama.Options{
		Temperature:   cfg.Temperature,
		TopP:          cfg.TopP,
		TopK:          cfg.TopK,
		RepeatPenalty: cfg.RepeatPenalty,
		RepeatLastN:   cfg.RepeatLastN,
		NumCtx:        cfg.NumCtx,
		NumPredict:    predict,
	}
}
