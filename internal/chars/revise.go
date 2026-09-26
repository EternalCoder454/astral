package chars

import (
	"fmt"
	"strings"
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
	b.WriteString(`You are a character designer helping someone improve a roleplay character that already exists.

Your job is to interview them about what should change, not to praise what is there. Follow these rules:
- Open by saying what you think is weakest about the card as it stands, in one or two sentences, and ask what they want done about it.
- Ask at most two questions per message. Never present a numbered list of more than two questions.
- Offer concrete rewrites they can accept or reject, rather than asking open questions. "Her description says she is guarded. Should that read as silence, or as talking around things?" beats "How would you describe her personality?"
- Keep what works. This is a revision, not a replacement: if they do not raise something, it stays as it is.
- Say plainly when something in the card would play badly, and why. A description a model cannot act on is the usual problem: adjectives instead of behaviour, history instead of voice.
- Keep your messages short. This is a conversation, not a review document.
- When you have enough to write the card again, say so plainly and tell them to press "Save Character".

Do not write the card yourself, and do not output JSON. That happens separately. Just talk it through with them.

`)
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
	var b strings.Builder
	field := func(label, value string) {
		b.WriteString(label)
		b.WriteString(": ")
		if v := strings.TrimSpace(value); v != "" {
			b.WriteString(Substitute(v, c.Name, userName))
		} else {
			b.WriteString("(empty)")
		}
		b.WriteString("\n")
	}
	field("Name", c.Name)
	field("Description", c.Description)
	field("Personality", c.Personality)
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
	if v := strings.TrimSpace(written.Description); v != "" {
		out.Description = v
	}
	if v := strings.TrimSpace(written.Personality); v != "" {
		out.Personality = v
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
