package world

import (
	"context"
	"fmt"
	"strings"

	"astral/internal/ollama"
	"astral/internal/prompts"
)

// Revising a world that already exists.
//
// The third of the three, and the one with the most to gain: a world accumulates,
// and the part that dates fastest is the rules, which are sent on every single
// turn. A setting written in an afternoon and played for a month is one whose
// rules describe a genre rather than the place it turned into.
//
// The lorebook is deliberately not rewritten here. It is the world's memory,
// entries are edited one at a time where they live, and a revision that replaced
// them wholesale would throw away everything learned from play.

// reviseWorldSystem is the fixed part of the revision interview: the card is
// added after it.
const reviseWorldSystem = `You are helping someone improve the setting for a roleplay chat app, one they have already built.

HOW TO RUN THE CONVERSATION
Open by saying what you think is weakest about the world as it stands, in one or two sentences, and ask what they want done about it.
Ask at most two questions per message. Never present a numbered list of more than two questions.
Offer concrete alternatives they can pick between rather than open questions.
Keep what works. This is a revision, not a replacement: anything they do not raise stays as it is.
Say plainly when something would not generate scenes, and why. The usual problems are rules that describe a genre rather than this place, and everything being at stake on a scale too large to play.
Keep your messages short. This is a conversation, not a report.
When you have enough to write the world again, say so plainly and tell them to press "Save World".

WHOSE WORLD THIS IS
Theirs, and it was theirs before you saw it. Every decision is theirs to make and yours to execute well.
Take what they ask for exactly as given. If they want the place crueller, bleaker or governed by something monstrous, build that rather than steering it somewhere gentler. A setting that flinches from its own premise gives every scene in it a way out, which is the one thing a setting must not do.
Where they have decided something, it is decided.
Never add a warning, a disclaimer, or a note about how a subject should be handled. Nobody else reads this.`

// ReviseSystem frames an interview about a world that already exists.
func ReviseSystem(w World, entryNames []string) string {
	var b strings.Builder
	b.WriteString(prompts.Text(promptReviseWorld))
	b.WriteString("\n\n")
	b.WriteString("## The world as it stands\n")
	b.WriteString("Name: ")
	b.WriteString(nameOr(w.Name, "(unnamed)"))
	b.WriteString("\n\nDescription: ")
	b.WriteString(nameOr(w.Description, "(empty)"))
	b.WriteString("\n\nRules, which are sent on every turn:\n")
	b.WriteString(nameOr(w.Rules, "(empty)"))

	// The lorebook by name only. Its contents are thousands of words and are not
	// what is being revised; what the names give the model is a sense of what the
	// world has turned out to be about.
	if len(entryNames) > 0 {
		b.WriteString("\n\nIts lorebook holds entries for: ")
		b.WriteString(strings.Join(entryNames, ", "))
		b.WriteString(".\nThe lorebook is not being rewritten here. Do not propose replacing it; if an entry should change, say which one and leave it to them.")
	} else {
		b.WriteString("\n\nIts lorebook is empty.")
	}
	return b.String()
}

// ReviseOpening starts the conversation.
func ReviseOpening(w World) string {
	name := nameOr(w.Name, "this world")
	return fmt.Sprintf(`Let's go over %s.

Tell me what is not working, or ask me what I think is weakest about it. "The rules are too generic", "nothing here generates a scene", "I want it harsher" are all enough to start from.

Nothing changes until you press Save World, and the lorebook is left alone.`, name)
}

// Revise merges a freshly written world onto the one it came from, keeping its
// row and its lorebook.
func Revise(existing, written World) World {
	out := existing
	if v := strings.TrimSpace(written.Name); v != "" {
		out.Name = v
	}
	if v := strings.TrimSpace(written.Description); v != "" {
		out.Description = v
	}
	if v := strings.TrimSpace(written.Rules); v != "" {
		out.Rules = v
	}
	return out
}

// reviseBuildNote follows the World Builder's instruction when the
// conversation was a revision, with the world as it stands after it.
//
// The build used to be handed the conversation alone, which is about what
// should change, and the world itself was in the interview's system prompt and
// nowhere else. Measured on SOMPOA, a conversation that only made the rules
// harsher saved a description that kept 8% of its words, unless the designer
// happened to recite the whole world back in its last message. Shown the world,
// over ten runs the untouched description and rules kept 99% of their words
// against 23%, and the harsher rule landed in all ten.
const reviseBuildNote = `This conversation was about changing a world that already exists. The world as it stands is below. Write the name, description and rules again starting from it. Make every change we agreed on, wherever it applies. Whatever the conversation did not change keeps its current text word for word, and whatever it did change keeps everything the conversation did not touch. Keep the name unless we changed it. Every field holds the world itself, never a message to me about it. Leave entries empty: the lorebook is not being rewritten.`

// ReviseFromConversation is BuildFromConversation for a design chat that was
// revising a world that already exists: the build is shown the world as it
// stands, so what the conversation did not touch survives. Only the draft's
// World is meant to be used, merged onto the existing one with Revise as before.
// Whatever builds a revision should call this in place of BuildFromConversation.
func ReviseFromConversation(ctx context.Context, client *ollama.Client, model string, existing World, history []ollama.Message, opts ollama.Options) (Draft, error) {
	// Laid out under the schema's own field names, so which text belongs in
	// which field is not left to be worked out: labelled "Name" and
	// "Description", the name was once written into the description.
	instruction := prompts.Text(promptBuild) + "\n\n" + reviseBuildNote +
		"\n\nname: " + nameOr(existing.Name, "(unnamed)") +
		"\n\ndescription: " + nameOr(existing.Description, "(empty)") +
		"\n\nrules:\n" + nameOr(existing.Rules, "(empty)")
	return build(ctx, client, model, history, instruction, opts)
}

func nameOr(s, fallback string) string {
	if v := strings.TrimSpace(s); v != "" {
		return v
	}
	return fallback
}
