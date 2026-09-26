package chars

import (
	"strconv"
	"strings"

	"astral/internal/ollama"
)

// A scene with several characters in it is one conversation, not several.
//
// The obvious build is a call per character: ask Vesper for a line, then
// Kestrel, then Ash. It is wrong in a way that is obvious the moment you read
// the output. Each character only ever sees a transcript in which nobody
// interrupts, so each one answers the user and ignores the others, and what you
// get is three monologues taking turns. The whole reason to put people in a room
// is that they talk to each other.
//
// So one call gets the whole cast, and the reply comes back with the speakers
// marked in it. That asks more of the model — it has to decide who reacts and
// who stays quiet — which is why the framing below spends most of its words on
// turn taking rather than on the characters.

// groupStructure is the group counterpart of framingStructure. The formatting
// rules are deliberately the same sentences: a scene should not change how it is
// written because a second person walked into it, and the renderer is the same
// renderer.
const groupStructure = `You are running a scene with several characters in it. You play all of them, and only them.

WHO SPEAKS
Mark every beat with the speaker's name, on its own line start, exactly like this:
%[1]s: *She did not look up from the chart.* "You're late."

%[2]s: "She's been saying that since noon."

Not everyone speaks every turn. One, two, or three of them react; the rest are present and quiet. Pick whoever would actually respond to what just happened, and let the others stay out of it.
Never give all of them one line each. A reply in which every character speaks exactly once, turn after turn, is the single thing that makes a scene like this read as a list rather than a conversation, and shuffling the order does not fix it.
They talk to each other, not only to %[3]s. Let them disagree, interrupt, answer each other's questions, and talk past %[3]s entirely when that is what would happen.

WHAT TO WRITE
Write only the words and actions of the characters listed below. Never write, decide, or narrate %[3]s's words, thoughts, or actions, wait for them.
Do not summarize the scene, do not skip ahead in time, and do not end the scene on your own.

FORMATTING. Inside a beat, every sentence you write is one of exactly two things, and there is no third kind:
1. Spoken aloud, in "double quotes". Nothing else goes inside quotes.
2. Everything else, meaning narration, action, body language, sensory detail and the character's own thoughts, inside *single asterisks*.
Never write an unmarked sentence. Every paragraph must start with the speaker's name, and what follows it is a quote or an asterisk.
Put a blank line between beats.`

// BuildGroupSystem assembles the system message for a scene with a cast.
//
// Each character gets their own block, and their own instructions go inside it
// rather than in the shared section at the end. A group's instructions cannot be
// pooled: "she always lies about her past" belongs to one of them, and pooled it
// becomes a scene where everybody lies.
func BuildGroupSystem(cast []Character, p Persona) string {
	cast = trimCast(cast)
	if len(cast) == 0 {
		return AssistantSystem
	}
	if len(cast) == 1 {
		return BuildSystem(cast[0], p)
	}
	userName := p.Name
	if userName == "" {
		userName = DefaultPersonaName
	}
	// Placeholders are expanded against the whole cast rather than one
	// character: {{char}} has no single referent here, so it becomes the list.
	// Left alone it would reach the model as the literal text, which a card
	// written for a two-hander would then have the character say out loud.
	names := CastNames(cast)
	allNames := strings.Join(names, ", ")
	sub := func(s string) string { return Substitute(s, allNames, userName) }

	var b strings.Builder
	head := strings.NewReplacer(
		"%[1]s", names[0],
		"%[2]s", names[1],
		"%[3]s", userName,
	).Replace(groupStructure)
	b.WriteString(strings.TrimSpace(head))

	b.WriteString("\n\nWHO IS HERE\n")
	b.WriteString(strings.Join(names, ", "))
	b.WriteString(". Nobody else is in this scene unless ")
	b.WriteString(userName)
	b.WriteString(" brings them into it.")

	b.WriteString("\n\nHOW TO WRITE IT\n")
	b.WriteString(sub(p.Style.Resolved()))
	b.WriteString("\n\n")
	b.WriteString(framingClose)

	for _, c := range cast {
		// Substituted per character inside their own block, so a card that says
		// "{{char}} never lies" means that character and not the whole cast.
		one := func(s string) string { return Substitute(s, c.Name, userName) }
		b.WriteString("\n\n## ")
		b.WriteString(c.Name)
		if d := strings.TrimSpace(c.Description); d != "" {
			b.WriteString("\n")
			b.WriteString(one(d))
		}
		if v := strings.TrimSpace(c.Personality); v != "" {
			b.WriteString("\nPersonality: ")
			b.WriteString(one(v))
		}
		if v := strings.TrimSpace(c.Instructions); v != "" {
			b.WriteString("\nHow to play ")
			b.WriteString(c.Name)
			b.WriteString(", from the user, and to be followed exactly: ")
			b.WriteString(one(v))
		}
	}

	// One scenario for the scene, not one per character. Cards each carry
	// their own opening situation and they will contradict each other, so the
	// first one that has anything to say sets the scene and the rest are
	// dropped: a scene cannot start in two places.
	for _, c := range cast {
		if v := strings.TrimSpace(c.Scenario); v != "" {
			b.WriteString("\n\n## Scenario\n")
			b.WriteString(Substitute(v, c.Name, userName))
			break
		}
	}

	if d := strings.TrimSpace(p.Description); d != "" {
		b.WriteString("\n\n## ")
		b.WriteString(userName)
		b.WriteString("\n")
		b.WriteString(sub(d))
	}

	if ins := strings.TrimSpace(p.GlobalInstructions); ins != "" {
		b.WriteString("\n\n## Instructions\nThese come from the user and take priority over the general guidance above. Follow them exactly.\n")
		b.WriteString(sub(ins))
	}
	return b.String()
}

// GroupAnchor is the closing block for a group scene: the last thing in the
// context before the model writes.
//
// It restates what drifts, and in a group what drifts first is the turn taking.
// By turn ten a model that has settled into a roll call will keep producing one,
// because its own transcript is the strongest instruction it can see — so the
// rule against that is here, in the position that is actually obeyed, and not
// only in the system prompt where it has already been outvoted.
func GroupAnchor(cast []Character, sc Scene, userName string) string {
	cast = trimCast(cast)
	if len(cast) == 0 {
		return ""
	}
	if len(cast) == 1 {
		return Anchor(cast[0], sc, userName)
	}
	names := CastNames(cast)

	var b strings.Builder
	b.WriteString("[Before you write, re-read this. It applies to your next reply and outranks the pattern of the messages above.\n\n")

	b.WriteString("You are playing ")
	b.WriteString(strings.Join(names, ", "))
	b.WriteString(". Never write, decide or narrate ")
	b.WriteString(userName)
	b.WriteString("'s words, thoughts or actions.\n\n")

	b.WriteString("WHO SPEAKS. Start every beat with the speaker's name and a colon, like \"")
	b.WriteString(names[0])
	b.WriteString(": \". ")
	if sc.RollCall {
		// Only when it is already happening. Said every turn it is noise, and
		// a model told not to do a thing it was not doing sometimes starts.
		b.WriteString("Your recent replies have given every character exactly one line each, which reads as a list rather than a scene. Correct that now: choose the ")
		b.WriteString(strconv.Itoa(minSpeakers(len(cast))))
		b.WriteString(" or so who would actually react to what just happened, in whatever order that means, and leave the others silent this turn. ")
	} else {
		b.WriteString("Not everyone speaks. Choose whoever would actually react and leave the rest silent. ")
	}
	b.WriteString("Have them respond to each other and not only to ")
	b.WriteString(userName)
	b.WriteString(".\n\n")

	if sc.NarrationDrifted {
		b.WriteString(anchorFormatFirm)
	} else {
		b.WriteString(anchorFormat)
	}
	b.WriteString("\n\n")

	if sc.StyleChanged {
		b.WriteString("STYLE. This has changed. The messages above were written to a different style; do not imitate them. From this reply on, write like this:\n")
	} else {
		b.WriteString("STYLE. Follow this exactly, even where the messages above do not:\n")
	}
	allNames := strings.Join(names, ", ")
	b.WriteString(Substitute(sc.Persona.Style.Resolved(), allNames, userName))

	if ins := strings.TrimSpace(sc.Persona.GlobalInstructions); ins != "" {
		b.WriteString("\n\nThe user's own instructions, which outrank everything else here:\n")
		b.WriteString(Substitute(ins, allNames, userName))
	}

	if d := strings.TrimSpace(sc.Direction); d != "" {
		b.WriteString("\n\nDIRECTION. Where the user wants this scene to go. Your next reply " +
			"must take a visible step toward it: have someone say or do something that " +
			"moves it along, in this reply, not a later one. Do not state the direction itself " +
			"and do not have anyone name it outright, and do not resolve the whole thing at " +
			"once. One step, now:\n")
		b.WriteString(Substitute(d, allNames, userName))
	}
	b.WriteString("]")
	return b.String()
}

// minSpeakers is how many of a cast should be speaking in a turn, as a number to
// put in front of a model that has been writing all of them. Half, rounded down,
// and never fewer than one: the point is fewer than all.
func minSpeakers(n int) int {
	if n <= 2 {
		return 1
	}
	return n / 2
}

// BuildGroupMessages assembles the full request for a group scene.
//
// The ordering is the same as BuildMessages and for the same reason: what does
// not change between turns goes first, so the server can reuse what it already
// computed, and what changes every turn goes last.
func BuildGroupMessages(cast []Character, sc Scene) []ollama.Message {
	cast = trimCast(cast)
	if len(cast) < 2 {
		var one Character
		if len(cast) == 1 {
			one = cast[0]
		}
		return BuildMessages(one, sc)
	}
	p := sc.Persona
	userName := p.Name
	if userName == "" {
		userName = DefaultPersonaName
	}
	allNames := strings.Join(CastNames(cast), ", ")
	system := BuildGroupSystem(cast, p)
	budget := sc.Budget
	if budget == (Budget{}) {
		budget = Plan(DefaultNumCtx, 0, len(system))
	}

	msgs := []ollama.Message{{Role: ollama.RoleSystem, Content: system}}

	if r := strings.TrimSpace(truncateTo(sc.Recap, budget.Recap)); r != "" {
		msgs = append(msgs, ollama.Message{
			Role: ollama.RoleSystem,
			Content: "Earlier in this scene. These are notes, not prose: they are written plainly " +
				"on purpose and are not an example of how to write. Treat all of it as " +
				"established fact, and do not copy the way it is written.\n" +
				Substitute(r, allNames, userName),
		})
	}

	// No example dialogue. Every card carries its own, written for a scene with
	// one character in it and no labels on the lines, so sending several of them
	// teaches the model both that labels are optional and that the other
	// characters are not there. The cast's voices have to come from their
	// descriptions until the transcript can do the job.

	history := trimHistory(sc.History, budget.History)
	for _, m := range history {
		m.Content = Substitute(m.Content, allNames, userName)
		msgs = append(msgs, m)
	}

	if lore := strings.TrimSpace(truncateTo(sc.Lore, budget.Lore)); lore != "" {
		msgs = append(msgs, ollama.Message{
			Role: ollama.RoleSystem,
			Content: "Reference for this world. These are established facts, true throughout, " +
				"not something that has just been said. Like the record above they are " +
				"notes rather than prose, and are not an example of how to write.\n" +
				Substitute(lore, allNames, userName),
		})
	}
	if a := GroupAnchor(cast, sc, userName); a != "" {
		msgs = append(msgs, ollama.Message{Role: ollama.RoleSystem, Content: a})
	}
	return msgs
}

// CastNames is the cast's names in order, for the prompt and for the splitter
// that reads the reply back.
func CastNames(cast []Character) []string {
	out := make([]string, 0, len(cast))
	for _, c := range cast {
		if n := strings.TrimSpace(c.Name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// trimCast drops members with no name. A character with no name cannot be
// labelled, so it cannot be told apart in the reply, and leaving it in the
// prompt would ask the model to mark beats with nothing.
func trimCast(cast []Character) []Character {
	out := make([]Character, 0, len(cast))
	for _, c := range cast {
		if strings.TrimSpace(c.Name) != "" {
			out = append(out, c)
		}
	}
	return out
}

// RollCall reports whether the recent replies have had every character speaking
// exactly once.
//
// That is the failure mode of a group scene, and it is self-sustaining: once two
// replies look like a list the transcript is teaching the model to write lists.
// Catching it lets the anchor argue against it only when it is happening, which
// is both cheaper and more effective than saying so every turn.
//
// Order is deliberately not part of the test. Measured over six-turn scenes, a
// model that had settled into a list varied the order freely while still giving
// everyone exactly one line, and a scene where three people speak but one of
// them speaks twice is an argument rather than a roll call.
//
// It wants the last few assistant turns, oldest first, each one a whole reply
// with its labels still on.
func RollCall(replies []string, names []string) bool {
	if len(names) < 3 {
		// With two characters, both speaking every turn is a conversation.
		return false
	}
	checked := 0
	for i := len(replies) - 1; i >= 0 && checked < 2; i-- {
		beats := SplitBeats(replies[i], names)
		if len(beats) != len(names) {
			return false
		}
		seen := make(map[string]bool, len(names))
		for _, b := range beats {
			if b.Name == "" || seen[b.Name] {
				return false // somebody spoke twice, or nobody did
			}
			seen[b.Name] = true
		}
		if len(seen) != len(names) {
			return false
		}
		checked++
	}
	return checked == 2
}
