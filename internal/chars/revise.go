package chars

import (
	"context"
	"fmt"
	"strings"

	"astral/internal/ollama"
	"astral/internal/prompts"
)

// Revising a character that already exists.
//
// The designer could only ever make a new one, which leaves the obvious case
// unanswered: a character written months ago, or written before the designer's
// instructions were what they are now, whose description you want brought up to
// the standard everything else is at. Deleting them and starting again loses the
// scenes they are in.
//
// So the interview runs again with the card already on the table. What comes out
// replaces the text and keeps the identity: the same row, the same picture, the
// same world, so every scene they are in carries on with a better version of the
// person in it.

// reviseCharacterSystem is the fixed part of the revision interview: the card is
// added after it.
const reviseCharacterSystem = `You are a character designer helping someone improve a roleplay character that already exists.

Your job is to interview them about what should change, not to praise what is there. Follow these rules:
- Open by saying what you think is weakest about the card as it stands, in one or two sentences, and ask what they want done about it.
- Ask at most two questions per message. Never present a numbered list of more than two questions.
- Offer concrete rewrites they can accept or reject, rather than asking open questions. "Her description says she is guarded. Should that read as silence, or as talking around things?" beats "How would you describe her personality?"
- Keep what works. This is a revision, not a replacement: if they do not raise something, it stays as it is.
- Say plainly when something in the card would play badly, and why. A description a model cannot act on is the usual problem: adjectives instead of behaviour, history instead of voice.
- Keep your messages short. This is a conversation, not a review document.
- When you have enough to write the card again, say so plainly and tell them to press "Save Character".

WHOSE CHARACTER THIS IS
Theirs, and it was theirs before you saw it. Every decision is theirs to make and yours to execute well.
Take what they ask for exactly as given. Do not soften it, do not redirect it, and do not offer a tamer version. If they want this character colder, crueller, more explicit or less sympathetic, write that; a revision that pulls a character back towards the middle is a revision that undoes the reason they made them.
Where they have decided something, it is decided, including the parts you would have written differently. Say what you think before they decide and stop after.
Never add a warning, a disclaimer, a moral, or a suggestion that the character be made more likeable. Nobody else reads this.
Your one job besides listening is craft: behaviour over adjectives, a voice that is theirs, something at stake. Say when a field would play badly and say why in terms of what the model will do with it.

Do not write the card yourself, and do not output JSON. That happens separately. Just talk it through with them.`

// ReviseSystem frames an interview about a character who already exists.
//
// The card is stated in full and marked as the starting point rather than as
// something to be defended. A model given a card and asked to help will otherwise
// spend the first three replies telling you how good it already is.
func ReviseSystem(c Character, p Persona) string {
	userName := strings.TrimSpace(p.Name)
	if userName == "" {
		userName = DefaultPersonaName
	}
	var b strings.Builder
	b.WriteString(prompts.Text(promptReviseCharacter))
	b.WriteString("\n\n")
	b.WriteString("## The character as it stands\n")
	b.WriteString(describeCard(c, userName))
	b.WriteString("\n\nThe person playing opposite this character is written {{user}}, and the character themselves {{char}}.")
	return b.String()
}

// describeCard lays a character out for the model to read, field by field, with
// the empty fields named rather than omitted.
//
// Naming them matters more than it looks. A field that is simply absent reads as
// a field that does not exist, and the one thing a revision should notice is that
// this character has no example dialogue and would be better with some.
func describeCard(c Character, userName string) string {
	return describeCardWith(c, func(s string) string { return Substitute(s, c.Name, userName) })
}

// describeCardWith is describeCard with each value passed through sub, so the
// card can be laid out with its placeholders filled in for the interview, or
// left as they are for the build, which has to write them back.
func describeCardWith(c Character, sub func(string) string) string {
	var b strings.Builder
	field := func(label, value string) {
		b.WriteString(label)
		b.WriteString(": ")
		if v := strings.TrimSpace(value); v != "" {
			b.WriteString(sub(v))
		} else {
			b.WriteString("(empty)")
		}
		b.WriteString("\n")
	}
	field("Name", c.Name)
	field("Age", c.Age)
	field("Gender", c.Gender)
	field("Race", c.Race)
	field("Occupation", c.Occupation)
	field("Relationship to {{user}}", c.Relationship)
	field("Description", c.Description)
	field("Personality", c.Personality)
	field("Appearance", c.Appearance)
	field("How they talk", c.Speech)
	field("Scenario", c.Scenario)
	field("Opening message", c.FirstMes)
	field("Example dialogue", c.MesExample)
	field("Instructions for playing them", c.Instructions)
	if len(c.Tags) > 0 {
		field("Tags", strings.Join(c.Tags, ", "))
	} else {
		field("Tags", "")
	}
	if n := len(c.AltGreetings); n > 0 {
		b.WriteString(fmt.Sprintf("Alternate openings: %d\n", n))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ReviseOpening is the designer's first message, so a blank page is never the
// user's problem to solve.
//
// It does not say what is wrong with the card, because it is written by the app
// rather than by the model and has not read it. Asking the question is what gets
// the model to answer it in its own first reply.
func ReviseOpening(c Character) string {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = "this character"
	}
	return fmt.Sprintf(`Let's go over %s.

Tell me what is not working, or ask me what I think is weakest about the card as it stands. "The description is all adjectives", "she sounds like everyone else", "I want her colder" are all enough to start from.

Nothing changes until you press Save Character, and anything we do not discuss stays as it is.`, name)
}

// reviseBuildNote follows the Character Builder's instruction when the
// conversation was a revision, with the card as it stands after it.
//
// The build used to be handed the conversation alone, and a revision
// conversation is about what should change: the card itself is in the
// interview's system prompt and nowhere else. So everything nobody mentioned was
// written fresh. Measured on SOMPOA with "I just want her meaner, nothing else
// changes", the saved card kept 9% of her appearance's words and 22% of her
// voice's, and moved her from a dockside bar to a private office. Shown the
// card, over three runs of four revisions (two characters, a style, a world),
// what was saved kept 96% of the words nobody asked to change against 14%,
// kept the name in twelve of twelve against seven, and made the change that was
// asked for in all twelve either way.
const reviseBuildNote = `This conversation was about changing a character who already exists. The card as it stands is below. Write the whole card again starting from it. Make every change we agreed on, in every field it affects. A field the conversation did not change keeps its current text word for word, and a field it did change keeps everything the conversation did not touch. Only a field marked (empty) is written fresh, from what we discussed. Keep the name unless we changed it. Every field holds the character, never a message to me about them.`

// ReviseFromConversation is BuildFromConversation for a design chat that was
// revising a character who already exists: the build is shown the card as it
// stands, so what the conversation did not touch survives. What it returns is
// the rewritten card, to be merged onto the existing one with Revise as before.
// Whatever builds a revision, the window or the phone, should call this in
// place of BuildFromConversation.
func ReviseFromConversation(ctx context.Context, client *ollama.Client, model string, existing Character, history []ollama.Message, opts ollama.Options) (Character, error) {
	instruction := prompts.Text(promptBuild) + "\n\n" + reviseBuildNote + "\n\n" +
		describeCardWith(existing, func(s string) string { return s })
	return buildCard(ctx, client, model, history, instruction, opts)
}

// Revise merges a freshly written card onto the character it came from.
//
// Identity is kept and text is replaced. The row, the pictures, the world, the
// tint and the date it was made all survive, because a revision has to leave every
// scene this character is in still pointing at them. What the interview did not
// cover survives too: the schema the model answers does not include instructions
// or alternate openings, so those come through untouched rather than being lost to
// a conversation that never mentioned them.
func Revise(existing, written Character) Character {
	out := existing

	if n := strings.TrimSpace(written.Name); n != "" {
		out.Name = n
	}
	for _, f := range []struct {
		to   *string
		from string
	}{
		{&out.Age, written.Age}, {&out.Gender, written.Gender}, {&out.Race, written.Race},
		{&out.Occupation, written.Occupation}, {&out.Relationship, written.Relationship},
	} {
		if v := strings.TrimSpace(f.from); v != "" {
			*f.to = v
		}
	}
	if v := strings.TrimSpace(written.Description); v != "" {
		out.Description = v
	}
	if v := strings.TrimSpace(written.Personality); v != "" {
		out.Personality = v
	}
	if v := strings.TrimSpace(written.Appearance); v != "" {
		out.Appearance = v
	}
	if v := strings.TrimSpace(written.Speech); v != "" {
		out.Speech = v
	}
	if v := strings.TrimSpace(written.Scenario); v != "" {
		out.Scenario = v
	}
	if v := strings.TrimSpace(written.FirstMes); v != "" {
		out.FirstMes = v
	}
	if v := strings.TrimSpace(written.MesExample); v != "" {
		out.MesExample = v
	}
	if len(written.Tags) > 0 {
		out.Tags = written.Tags
	}
	return out
}
