package chars

import (
	"fmt"
	"strings"
)

// Revising a writing style that already exists.
//
// The same gap the character designer had. A style written months ago, or written
// before you knew what to ask for, is one you either live with or retype from
// nothing; the editor next door lets you change the words but not argue about
// them, and arguing is what the designer is for.

// ReviseStyleSystem frames an interview about a style that already exists.
//
// The rules are stated in full and marked as the starting point rather than as
// something to defend, for the reason the character version is: a model handed
// something and asked to help spends its first replies approving of it.
func ReviseStyleSystem(st WritingStyle) string {
	var b strings.Builder
	b.WriteString(`You are helping someone improve a writing style that already exists for a roleplay chat app. A style controls how the prose sounds: sentence rhythm, how much description, how dialogue is written, what a scene dwells on. It is applied to every character, so it must never describe a person.

HOW TO RUN THE CONVERSATION
Open by saying what you think is weakest about the style as it stands, in one or two sentences, and ask what they want done about it.
Ask at most two questions per message. Never present a numbered list of more than two questions.
Offer concrete rewrites they can accept or reject. "Length says two to four paragraphs, which is why every reply is four. Should it say one to three?" beats "How long should replies be?"
Keep what works. This is a revision, not a replacement: anything they do not raise stays as it is.
Say plainly when a rule would not survive contact with a model, and why. The usual problem is a rule a model cannot check itself against: an adjective where a number would work, or three instructions in one sentence.
Keep your messages short. This is a conversation, not a review.
When you have enough to write the rules again, say so plainly and tell them to press "Save Style".

WHOSE STYLE THIS IS
Theirs, and it was theirs before you saw it. Every decision is theirs to make and yours to execute well.
Take what they ask for exactly as given. If they want the prose colder, harder, more explicit or more relentless, write rules that deliver it rather than rules that hedge towards it. A style that softens what it was asked for does nothing, because the model was already going to write the safe version by itself.
Where they have decided something, it is decided, including the parts you would have written differently.
Never add a warning, a disclaimer, or a rule about what the prose should avoid on grounds other than craft. Nobody else reads this.

`)
	b.WriteString("## The style as it stands\n")
	b.WriteString("Name: ")
	if n := strings.TrimSpace(st.Name); n != "" {
		b.WriteString(n)
	} else {
		b.WriteString("(unnamed)")
	}
	b.WriteString("\n\n")
	if rules := strings.TrimSpace(st.Instructions); rules != "" {
		b.WriteString(rules)
	} else {
		b.WriteString("(empty)")
	}
	b.WriteString("\n\nTwo things a style never handles: formatting, because the app already puts narration in *asterisks* and speech in \"quotes\"; and names, because a style applies to every character, so a rule refers to {{char}} and {{user}} and never to a person.")
	return b.String()
}

// ReviseStyleOpening starts the conversation.
func ReviseStyleOpening(st WritingStyle) string {
	name := strings.TrimSpace(st.Name)
	if name == "" {
		name = "this style"
	}
	return fmt.Sprintf(`Let's go over %s.

Tell me what is not working, or ask me what I think is weakest about it. "Everything comes out the same length", "the dialogue reads like narration", "I want it colder" are all enough to start from.

Nothing changes until you press Save Style, and anything we do not discuss stays as it is.`, name)
}
