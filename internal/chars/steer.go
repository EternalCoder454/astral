package chars

import (
	"encoding/json"
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

// PaceBlock is the pace rule for the closing block, with names in, and the
// scene's setting line before it when it has one: the rule says to stay in
// the moment, and the line says which moment that is.
func PaceBlock(charName, userName string) string {
	return "\n\n" + Substitute(prompts.Text(promptPace), charName, userName)
}

// SettingChars bounds a scene's setting line, which is sent every turn.
const SettingChars = 200

// SceneState is how a scene stands beyond where and when it is: what the
// people in it are wearing and holding, how things stand between them, and
// what is still unresolved. Sent every turn with the setting, and kept up to
// date after each reply the same way. See scene.TrackState.
//
// It is the most asked for kind of SillyTavern extension (Tracker, Doom's
// Enhancement Suite), for the drift it answers: a mid-size model forty turns
// on has her coat back on, the key in the wrong hand, and last hour's quarrel
// forgotten, because the turns that settled them have scrolled out of what it
// reads. A few short lines it reads every turn keep them.
type SceneState struct {
	Wearing    string `json:"wearing,omitempty"`
	Holding    string `json:"holding,omitempty"`
	Between    string `json:"between,omitempty"`
	Unresolved string `json:"unresolved,omitempty"`
}

// StateChars bounds each part of a scene's state, which is sent every turn.
const StateChars = 160

// StateField is one part of a scene's state, as the model and the person see
// it: the key it has in JSON, and its label in the closing block and in
// Scene Memory.
type StateField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

// stateParts is how many StateFields there are, as a constant, for the
// budget. TestStatePartsCountsTheFields holds the two together.
const stateParts = 4

// StateFields are the parts of a scene's state, in the order they are shown.
var StateFields = []StateField{
	{"wearing", "Wearing", "What each person in the scene has on, briefly."},
	{"holding", "Holding", "What anyone has in hand or with them that matters."},
	{"between", "Between You", "How things stand between the characters and you right now."},
	{"unresolved", "Unresolved", "What has been started or promised and not yet settled."},
}

// Get is a part of the state by its key.
func (s SceneState) Get(key string) string {
	switch key {
	case "wearing":
		return s.Wearing
	case "holding":
		return s.Holding
	case "between":
		return s.Between
	case "unresolved":
		return s.Unresolved
	}
	return ""
}

// Set changes a part of the state by its key, bounded.
func (s *SceneState) Set(key, value string) {
	value = strings.TrimSpace(value)
	if r := []rune(value); len(r) > StateChars {
		value = string(r[:StateChars])
	}
	switch key {
	case "wearing":
		s.Wearing = value
	case "holding":
		s.Holding = value
	case "between":
		s.Between = value
	case "unresolved":
		s.Unresolved = value
	}
}

// Empty reports whether nothing is recorded.
func (s SceneState) Empty() bool {
	return s == SceneState{}
}

// stateLabel is how a part of the state is labelled for the model, which is
// plainer than the label a person sees.
var stateLabel = map[string]string{
	"wearing":    "Wearing",
	"holding":    "Holding",
	"between":    "Between {{char}} and {{user}}",
	"unresolved": "Unresolved",
}

// SettingBlock is the closing block's part saying where and when the scene
// is, and how it stands.
//
// Framed as a record to keep to rather than material to use: a model told
// what everyone is wearing will otherwise describe it in every reply.
func SettingBlock(setting string, state SceneState, charName, userName string) string {
	setting = strings.TrimSpace(setting)
	if r := []rune(setting); len(r) > SettingChars {
		setting = string(r[:SettingChars])
	}
	if setting == "" && state.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nNOW. How the scene stands at this moment, for continuity. Keep to it unless the scene itself moves on. It is a record, not a list of things to mention.")
	if setting != "" {
		b.WriteString("\nWhere and when: ")
		b.WriteString(setting)
	}
	for _, f := range StateFields {
		if v := strings.TrimSpace(state.Get(f.Key)); v != "" {
			b.WriteString("\n")
			b.WriteString(stateLabel[f.Key])
			b.WriteString(": ")
			b.WriteString(v)
		}
	}
	return Substitute(b.String(), charName, userName)
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

// suggestAnchor replaces the closing block to offer three things you could
// do next. Three rather than one, because what they are for is choice: Write
// for Me is the button that writes one.
const suggestAnchor = `[This time you are not writing {{char}}. Suggest three different things {{user}} could say or do next in this scene, for them to pick from.

Each is a complete message written as {{user}}, exactly as they would type it themselves: their person, so if they write *I lean in* you write I and never you, their tense, their way of marking speech and action, and about their usual length. Make the three truly different: one that answers what was just said or done, one that takes the scene somewhere new, and one bolder than the other two. Only what {{user}} says and does; never a line or an action for {{char}}.]`

// stateAnchor replaces the closing block to keep the record of how the scene
// stands. {{record}} is the record as it is, and {{ask}} either asks for what
// the latest exchange changed, after a reply, or for everything, when you
// ask for a suggestion.
const stateAnchor = `[This time you are not writing {{char}}. You are keeping the record of how this scene stands, for continuity. The record as it is:
{{record}}

{{ask}}
- where: where and when the scene is: the place, and the time of day.
- wearing: what each person in the scene has on, by name.
- holding: what anyone has in hand or with them that matters, by name.
- between: how things stand between {{char}} and {{user}} right now.
- unresolved: what has been started or promised and not yet settled.
Each part is plain facts in at most twenty words, as things are now, with nothing about what should happen next.]`

// stateAskChanged and stateAskAll are the two things stateAnchor asks.
const (
	stateAskChanged = "Write only the parts the latest exchange changed, and any that are empty and that the scene has now shown, each rewritten whole. Leave out every part that is still right."
	stateAskAll     = "Write every part, as the scene stands now."
)

var (
	promptSuggest = prompts.Register(prompts.Prompt{
		ID: "scene.suggest", Name: "Suggested Replies", Group: "Scenes",
		About: "Sent in place of the closing block when you ask for suggestions of what to say next. " +
			"The answer is three messages, as a list.",
		Keep:    "{{char}} becomes the character's name, or the whole cast's, and {{user}} yours.",
		Default: suggestAnchor,
		Anchors: []string{"you are not writing {{char}}", "never a line or an action for {{char}}"},
	})
	promptState = prompts.Register(prompts.Prompt{
		ID: "scene.state", Name: "Scene State", Group: "Scenes",
		About: "Sent in place of the closing block after each reply, to keep where and when the scene is, " +
			"what everyone is wearing and holding, how things stand and what is unresolved, and when " +
			"Scene Memory suggests them.",
		Keep: "{{record}} and {{ask}} are filled in by Astral. The part names (where, wearing, holding, " +
			"between, unresolved) are the answer's and must stay. {{char}} becomes the character's name, " +
			"or the whole cast's, and {{user}} yours.",
		Default: stateAnchor,
		Anchors: []string{"you are not writing {{char}}", "{{record}}", "{{ask}}"},
	})
)

// SuggestSchema is the answer to Suggested Replies: three messages.
var SuggestSchema = json.RawMessage(`{"type":"object","properties":{"options":{"type":"array","items":{"type":"string"},"minItems":3,"maxItems":3}},"required":["options"]}`)

// swapClosing replaces a scene request's closing block with another.
func swapClosing(msgs []ollama.Message, text string) []ollama.Message {
	out := append([]ollama.Message(nil), msgs...)
	if n := len(out); n > 0 && out[n-1].Role == ollama.RoleSystem && strings.HasPrefix(out[n-1].Content, "[") {
		out = out[:n-1]
	}
	return append(out, ollama.Message{Role: ollama.RoleSystem, Content: text})
}

// SuggestMessages turns a scene's request into one for three suggestions.
func SuggestMessages(msgs []ollama.Message, charName, userName string) []ollama.Message {
	text := Substitute(prompts.Text(promptSuggest), charName, userName)
	if words, paras := yourShape(msgs); words > 0 {
		text = strings.TrimSuffix(text, "]") + "\n" + fmt.Sprintf(yourLength, userName, words, paras, plural(paras, "paragraph")) + "]"
	}
	return swapClosing(msgs, text)
}

// ParseSuggestions reads the three suggestions, tidied, leaving out blanks
// and repeats.
func ParseSuggestions(raw []byte, userName string) []string {
	var out struct {
		Options []string `json:"options"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var list []string
	for _, o := range out.Options {
		o = CleanDraft(o, userName)
		if o == "" || seen[strings.ToLower(o)] {
			continue
		}
		seen[strings.ToLower(o)] = true
		list = append(list, o)
	}
	return list
}

// StateMessages turns a scene's request into one keeping the record of how
// it stands: every part when all is set, and otherwise only what the latest
// exchange changed.
func StateMessages(msgs []ollama.Message, setting string, state SceneState, charName, userName string, all bool) []ollama.Message {
	var record strings.Builder
	line := func(key, value string) {
		if strings.TrimSpace(value) == "" {
			value = "(empty)"
		}
		record.WriteString(key + ": " + value + "\n")
	}
	line("where", setting)
	for _, f := range StateFields {
		line(f.Key, state.Get(f.Key))
	}
	ask := stateAskChanged
	if all {
		ask = stateAskAll
	}
	text := strings.NewReplacer("{{record}}", strings.TrimSpace(record.String()), "{{ask}}", ask).
		Replace(prompts.Text(promptState))
	return swapClosing(msgs, Substitute(text, charName, userName))
}

// StateSchema is the answer to StateMessages: any of the parts, or, when all
// is set, every one.
func StateSchema(all bool) json.RawMessage {
	keys := []string{"where"}
	for _, f := range StateFields {
		keys = append(keys, f.Key)
	}
	props := make([]string, len(keys))
	for i, k := range keys {
		props[i] = `"` + k + `":{"type":"string"}`
	}
	schema := `{"type":"object","properties":{` + strings.Join(props, ",") + `}`
	if all {
		schema += `,"required":["` + strings.Join(keys, `","`) + `"]`
	}
	return json.RawMessage(schema + "}")
}

// ApplyState reads an answer to StateMessages onto a setting and state, and
// reports whether it changed either. A part left out, or left blank, stays
// as it was.
func ApplyState(raw []byte, setting string, state SceneState) (string, SceneState, bool) {
	var got map[string]string
	if json.Unmarshal(raw, &got) != nil {
		return setting, state, false
	}
	changed := false
	if v := CleanSetting(got["where"]); v != "" && v != strings.TrimSpace(setting) {
		setting, changed = v, true
	}
	for _, f := range StateFields {
		v := cleanStatePart(got[f.Key])
		if v != "" && v != strings.TrimSpace(state.Get(f.Key)) {
			state.Set(f.Key, v)
			changed = true
		}
	}
	return setting, state, changed
}

// cleanStatePart tidies one part of an answer: one line, no label, and none
// of the stand-ins a model writes for nothing.
func cleanStatePart(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", "; ")), " ")
	s = strings.Trim(s, `"* `)
	switch strings.ToLower(strings.TrimSuffix(s, ".")) {
	case "", "(empty)", "empty", "none", "unchanged", "n/a", "nothing":
		return ""
	}
	if r := []rune(s); len(r) > StateChars {
		s = string(r[:StateChars])
	}
	return s
}

// CleanSetting tidies a suggested setting line: one line, no label, bounded.
func CleanSetting(s string) string {
	s = strings.TrimSpace(s)
	if first, _, ok := strings.Cut(s, "\n"); ok {
		s = strings.TrimSpace(first)
	}
	for _, label := range []string{"Setting:", "NOW:", "Now:", "Where:"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, label))
	}
	s = strings.Trim(s, `"*`)
	if r := []rune(s); len(r) > SettingChars {
		s = string(r[:SettingChars])
	}
	return strings.TrimSpace(s)
}
