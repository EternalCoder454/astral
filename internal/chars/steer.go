package chars

import (
	"fmt"
	"strings"

	"astral/internal/ollama"
	"astral/internal/prompts"
)

// Three ways of steering one turn without changing the scene's standing
// instructions, each something every other roleplay app offers and Astral did
// not: writing your turn for you, writing a reply again toward a note, and a
// group carrying on without you.
//
// All three reuse the scene's own prompt and change only its closing block.
// That is not only tidiness: everything before the closing block is the prefix
// the server has already computed for this scene, so a draft or a guided
// rewrite of a long scene starts writing in a moment instead of reading the
// whole scene again first.

// draftAnchor replaces the closing block when the model writes your turn.
//
// It opens by saying whose turn this is, because everything above it, the
// framing, the card, the rules, the transcript, says the model is the
// character and must never write you. The matching of your own messages is the
// rule that matters most: a draft in the character's prose style is a draft
// nobody sends.
const draftAnchor = `[This time you are not writing {{char}}. Write {{user}}'s next message in this scene, for them to read and send as their own.

Write only what {{user}} says and does: their words, their actions, what they notice. Never write a line or an action for {{char}} or anyone else, and never decide how anyone reacts.
Carry on from the last message: answer what was just said or done, and move the scene one step.
Write it the way {{user}} writes. Copy their own recent messages: the same length and the same number of paragraphs, the same person (I or they), the same tense, and the same way of marking speech and action. If they write one action and one line, write one action and one line. With nothing of theirs to go by, write two to four sentences, speech in "double quotes" and actions in *single asterisks*.
Only the message itself: no name in front of it, no preamble, no choices, no notes.]`

// draftFromIdea is added when you typed something before asking, or are
// rewriting a message you sent, which makes the request "write this better"
// rather than "write one for me". What you typed may be a finished message or
// a note of what you meant, and both come back as your message.
const draftFromIdea = "\n\n{{user}} has already written this, as a finished message or as a note of what they want to say or do. Write it out as their message and make it better: keep everything it says, keep every action in it as the action they wrote even where the scene suggests another, keep who it is aimed at, add nothing that changes what happens, fix the spelling and grammar, and make it read well:\n"

var (
	promptDraft = prompts.Register(prompts.Prompt{
		ID: "scene.draft", Name: "Write for Me", Group: "Scenes",
		About: "Sent in place of the closing block when Write for Me drafts your next message in a " +
			"scene. Everything before it is the scene's own prompt.",
		Keep: "{{char}} becomes the character's name, or the whole cast's, and {{user}} yours. It must " +
			"say plainly that this time the model is writing you, because everything above it says the opposite.",
		Default: draftAnchor,
		Anchors: []string{"you are not writing {{char}}", "Never write a line or an action for {{char}}"},
	})
	promptDraftIdea = prompts.Register(prompts.Prompt{
		ID: "scene.draft-idea", Name: "Rewrite My Message", Group: "Scenes",
		About:   "Added to Write for Me when there is text in the message box, or when a message you sent is rewritten. Your text follows it.",
		Slots:   []string{"{{user}}"},
		Default: draftFromIdea,
	})
	promptNote = prompts.Register(prompts.Prompt{
		ID: "scene.rewrite-note", Name: "Rewrite with a Note", Group: "Scenes",
		About: "Added at the very end of the closing block when a reply is written again with a note. " +
			"The note follows it.",
		Default: rewriteNote,
	})
	promptOnward = prompts.Register(prompts.Prompt{
		ID: "scene.onward", Name: "Let Them Talk", Group: "Scenes",
		About:   "Added to a group scene's closing block when the characters carry on without you.",
		Slots:   []string{"{{user}}"},
		Default: onwardNote,
	})
)

// paceRule keeps a reply in the moment the person left it in.
//
// The system prompt has always said not to skip ahead, and replies did anyway:
// asked to walk someone home, a character would walk, arrive, go in and be
// kissing them by the last paragraph. A rule stated once at the front loses
// to a style asking for two to four paragraphs, which a model fills by moving
// the story on. So it is restated here, in the closing block, in terms of a
// place and a minute rather than a principle.
const paceRule = "PACE. Go no further than {{user}}'s last message goes. Everything in your reply happens in the same place, within a minute or two of it: if they are on the way somewhere, your reply ends still on the way. Do not arrive, go inside, skip time or start the next thing, and do not take things between you further than {{user}} has. End on something {{user}} can answer in that same moment."

var promptPace = prompts.Register(prompts.Prompt{
	ID: "scene.pace", Name: "Pace", Group: "Scenes",
	About: "Part of the closing block of every scene, one-on-one or group, so a reply stays in the " +
		"moment you left it in instead of moving the story on by itself.",
	Keep:    "{{user}} becomes your name. It is stated as a place and a minute because a general rule against skipping ahead was ignored.",
	Default: paceRule,
	Anchors: []string{"Go no further than {{user}}'s last message goes", "Do not arrive, go inside, skip time"},
})

// PaceBlock is the pace rule for the closing block, with names in.
func PaceBlock(charName, userName string) string {
	return "\n\n" + Substitute(prompts.Text(promptPace), charName, userName)
}

// rewriteNote introduces a note on a reply being written again.
//
// Last in the closing block, after the direction, so it is the final thing
// read. It says the note is about this reply only, so "shorter" shortens one
// reply rather than turning into a standing rule the model keeps applying.
const rewriteNote = "\n\nTHIS REPLY. The user asked for this reply to be written again, and said what they want from it. For this one reply it outranks the style and everything else above, so make the change large enough to notice, and keep everything it does not touch:\n"

// onwardNote is a group turn nobody asked for.
const onwardNote = "\n\nNOBODY IS WAITING ON {{user}}. {{user}} says and does nothing this turn. The characters carry the scene on among themselves: pick up a thread from what just happened and move it forward. Do not have anyone ask {{user}} a question or wait for them to answer."

// NoteBlock is the closing block's final part for a guided rewrite, empty
// without a note.
func NoteBlock(note, charName, userName string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return ""
	}
	return prompts.Text(promptNote) + Substitute(note, charName, userName)
}

// DraftMessages turns a scene's request into one for your next message: the
// same prompt with its closing block replaced.
//
// idea is what you had already typed, or nothing.
func DraftMessages(msgs []ollama.Message, charName, userName, idea string) []ollama.Message {
	out := append([]ollama.Message(nil), msgs...)
	// The closing block is the last message when there is one, and it is the
	// one thing a draft must not keep: it tells the model to be the character.
	if n := len(out); n > 0 && out[n-1].Role == ollama.RoleSystem && strings.HasPrefix(out[n-1].Content, "[") {
		out = out[:n-1]
	}
	text := Substitute(prompts.Text(promptDraft), charName, userName)
	// Their length in numbers. Told only to match it, a model matched the
	// character's replies instead, which were in front of it far more often:
	// measured on SOMPOA, five drafts in eight ran past three times the
	// length of the turns they were meant to copy.
	words, paras := yourShape(msgs)
	// A message being rewritten keeps its own length when that is longer:
	// making it better is not making it shorter.
	if w, p := shapeOf(idea); w > words {
		words, paras = w, max(paras, p)
	}
	if words > 0 {
		text = strings.TrimSuffix(text, "]") + "\n" + fmt.Sprintf(yourLength, userName, words, paras, plural(paras, "paragraph")) + "]"
	}
	if idea = strings.TrimSpace(idea); idea != "" {
		text = strings.TrimSuffix(text, "]") + Substitute(prompts.Text(promptDraftIdea), charName, userName) + idea + "]"
	}
	return append(out, ollama.Message{Role: ollama.RoleSystem, Content: text})
}

// yourLength states the length of your own recent turns.
const yourLength = "%s's recent messages run to about %d words, in %d %s. Write about that much and no more."

// yourShape is the typical length of your recent turns: their mean word
// count and their most common number of paragraphs. Zero words when there
// are none to go by.
func yourShape(msgs []ollama.Message) (words, paras int) {
	counts := map[int]int{}
	n := 0
	for i := len(msgs) - 1; i >= 0 && n < 4; i-- {
		if msgs[i].Role != ollama.RoleUser {
			continue
		}
		w, p := shapeOf(HideAttachedFiles(msgs[i].Content))
		if w == 0 {
			continue
		}
		n++
		words += w
		counts[p]++
	}
	if n == 0 {
		return 0, 0
	}
	for p, c := range counts {
		if c > counts[paras] || (c == counts[paras] && p < paras) {
			paras = p
		}
	}
	return (words + n/2) / n, max(paras, 1)
}

// shapeOf is one message's words and paragraphs.
func shapeOf(s string) (words, paras int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0
	}
	for _, part := range strings.Split(s, "\n\n") {
		if strings.TrimSpace(part) != "" {
			paras++
		}
	}
	return len(strings.Fields(s)), paras
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// CleanDraft tidies a drafted message for the message box: the name label a
// model puts on a turn out of habit comes off, and so does a line of preamble.
func CleanDraft(s, userName string) string {
	s = strings.TrimSpace(s)
	for _, label := range []string{userName + ":", "**" + userName + ":**", "**" + userName + "**:"} {
		if userName != "" && strings.HasPrefix(s, label) {
			s = strings.TrimSpace(s[len(label):])
		}
	}
	// "Here is a message for Sam:" and its relatives, on a line of their own.
	if first, rest, ok := strings.Cut(s, "\n"); ok {
		f := strings.ToLower(strings.TrimSpace(first))
		if strings.HasSuffix(f, ":") && (strings.HasPrefix(f, "here") || strings.HasPrefix(f, "sure")) {
			s = strings.TrimSpace(rest)
		}
	}
	return s
}
