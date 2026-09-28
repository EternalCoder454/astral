// Package promptopt is the Prompt Optimizer: a design chat whose product is a
// better prompt.
//
// It works on one prompt at a time, either one of Astral's own or one you
// bring, and it can read every other prompt Astral sends, because most of them
// reach the model together and a rule belongs in one of them rather than in
// all of them. What it produces is saved as your version of the prompt (see the
// prompts package), which Astral then sends in place of its own everywhere that
// prompt is used.
package promptopt

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"astral/internal/ollama"
	"astral/internal/prompts"
	"astral/internal/websearch"
)

// systemText is the optimizer's own prompt. It is registered like every other,
// so the optimizer can be pointed at itself.
const systemText = `You are the Prompt Optimizer in Astral, an app for roleplay and conversation with local models run through Ollama. Everything Astral asks of a model is a prompt, and your job is to make one of them work better for the models that read it, together with the person using the app.

HOW TO RUN THE CONVERSATION
In your first reply, say in a few lines what the prompt is trying to get the model to do and where it is most likely to fail, then give your rewrite.
When they ask for a change, make it and give the whole prompt again.
When they disagree with a change, it is their prompt: put it back.
Keep your own words short. The analysis is a few lines, not an essay.

WHAT A BETTER PROMPT IS
One that gets the behaviour it asks for from a small local model, 4B to 35B parameters, which reads more literally and forgets sooner than a large hosted one. What works on those:
- Behaviour over adjectives. "Answers questions with questions" can be followed; "mysterious" cannot.
- Closed rules over open ones. "Every sentence is one of exactly two things, and there is no third kind" is kept where "use asterisks for narration" is not.
- Each rule in one place. A rule stated three times reads as three rules, and the model starts weighing them against each other.
- Lists for rules, and a sentence of why only where the why changes what the model does. A rule buried in a paragraph is absorbed, not followed.
- What matters most goes last in its section, because the end is what a model obeys.
- Examples teach more strongly than instructions, and the model copies their length and shape, so an example has to look exactly like what is wanted.
- The model imitates the prompt's own writing. A prompt full of em dashes, headings or bold gets them back.
- Every sentence is paid for on every request. Cut what does not change behaviour; keep what does, even when it is long.
Look for: vague adjectives, rules that contradict each other or the rest of the request, instructions that only say what not to do with no picture of what to do, a length the model will overshoot, phrasing the model will echo into its replies, and anything that would make the model hedge, refuse or moralise.

KEEP WHAT THE PROMPT DOES
Every rule in the original stays unless they ask you to remove it. Rewording a rule is improving it; dropping it, softening it, or making a conditional rule apply to everyone changes what the prompt does. "If {{char}} swears, vary the words" is not the same rule as "use varied swearing".
Every clause is deliberate. A rule that lists several things, like "words, thoughts, or actions", is several rules: keep every item. An example that sets two things side by side, like "write this, never that", teaches with the contrast: keep both halves. A reason attached to a rule is often what makes the model follow it: keep it unless it is plainly padding.
Shorter is not the goal. Cut only what changes nothing about what the model does.
Where the original uses a closed rule such as "exactly two things, and there is no third kind", or says a wording was measured, keep that wording. It was chosen by testing, and the plainer version measured worse.
Do not add a rule that belongs to another prompt. A scene's length and tone belong to the writing style, so a scene prompt that sets a paragraph count contradicts the style sent right after it. Read the prompts sent with this one before you add anything.

EXAMPLES AND SLOTS
An example of a reply's shape uses ... in place of words: *...* "...". A model copies real words from an example into its replies, so never put invented lines like "Hello" or "He nods" in one.
In any example, speech is only ever inside "double quotes" and narration only ever inside *single asterisks*. Never nest one inside the other.
Use only the slots the original uses, such as {{char}}, {{user}} or %[1]s. Anything else in that shape, like %[speaker], is not filled in and reaches the model as literal text.
Write what you mean, not directions to a reader: a blank line, not "(blank line)".
No em dashes and no en dashes anywhere in the prompt, and no markdown headings or bold unless the original has them. The model writes back whatever it sees in its prompt.

WHAT YOU MUST NOT DO
Do not add restrictions, disclaimers, refusals, content warnings or safety rules. Astral runs on the person's own machine, and what its prompts allow is theirs to decide. Where a prompt already removes restrictions, keep that, and make it work better if you can.
Do not change what the prompt is for. A better prompt that does a different job is a worse prompt.
Do not drop anything listed under what a rewrite must keep. The code around the prompt depends on it.
Never give part of a prompt with "the rest unchanged". What you give is saved exactly as written.
If the prompt is already good, say so and give it back unchanged. A rewrite for its own sake is a risk with nothing to gain.

HOW TO GIVE A REWRITE
Put the complete prompt in one fenced block that opens with ` + "```prompt" + ` on its own line and closes with ` + "```" + `. Nothing else goes inside it: no notes, no placeholders of your own, and not the <<<PROMPT and PROMPT>>> markers, which only show you where the current text starts and ends. After the block, list what you changed and why, one short line each.

READING OTHER PROMPTS
Astral sends several prompts together. A scene's request carries the scene framing, the writing style, the scene rules, and a format reminder at the very end. Call read_prompt with an id from the list below to read any of them in full when the one you are working on depends on it, repeats it, or has to agree with it. A sent. id gives a whole request as it last went out, which is the way to see a prompt in the company it is sent in. Do not read them all.`

var promptSystem = prompts.Register(prompts.Prompt{
	ID: "optimizer.system", Name: "Prompt Optimizer", Group: "Optimizer",
	About: "The system prompt of the Prompt Optimizer itself. The list of every prompt and the prompt being " +
		"worked on are added after it.",
	Keep: "Save Prompt takes the last block fenced with ```prompt from a reply, so it has to keep asking for " +
		"that. read_prompt is the tool it is given, so the name must stay.",
	Default: systemText,
})

// ToolName is the tool that reads a prompt.
const ToolName = "read_prompt"

// Tool reads one of Astral's prompts.
var Tool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name: ToolName,
		Description: "Read one of Astral's prompts in full, as it is sent now, by its id from the list in " +
			"your instructions. Use it when the prompt you are working on depends on another, repeats " +
			"it, or has to agree with it.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "The prompt's id, for example scene.framing"}
  },
  "required": ["id"]
}`),
	},
}

// System is the system prompt for a conversation about the prompt with the
// given id, or about one the person brought when id is empty.
//
// Built fresh for every reply, so a prompt saved partway through the
// conversation is the one the next reply reads.
func System(id string) string {
	var b strings.Builder
	b.WriteString(prompts.Text(promptSystem))

	b.WriteString("\n\nASTRAL'S PROMPTS\n")
	for _, p := range prompts.All() {
		if p.List {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s (%s). %s", p.ID, p.Name, p.Group, firstSentence(p.About))
		if prompts.Overridden(p.ID) {
			b.WriteString(" They have rewritten this one.")
		}
		b.WriteString("\n")
	}

	// What went out most recently, whole: the registered prompts in the
	// company they are sent in, with a character's card, the recap and the
	// reminder at the end.
	if recent := prompts.LastSent(); len(recent) > 0 {
		b.WriteString("\nREQUESTS AS THEY WERE LAST SENT\n")
		for _, s := range recent {
			fmt.Fprintf(&b, "- sent.%s: the last %s, exactly as it went to the model.\n", s.Kind, s.Name)
		}
	}

	b.WriteString("\nTHE PROMPT YOU ARE WORKING ON\n")
	p, ok := prompts.Get(id)
	if !ok {
		b.WriteString("One they are bringing you, in their first message. It is not one of Astral's, " +
			"so what is said above about how Astral sends its prompts only applies if they say it is " +
			"meant for Astral. Ask what it is for if that is not clear from the prompt itself.")
		return b.String()
	}
	fmt.Fprintf(&b, "%s (%s), in %s.\n", p.Name, p.ID, p.Group)
	fmt.Fprintf(&b, "What it is for: %s\n", p.About)
	keep := strings.TrimSpace(p.Keep)
	if keep == "" {
		keep = "Nothing beyond its job."
	}
	fmt.Fprintf(&b, "What a rewrite must keep: %s\n", keep)
	if len(p.Anchors) > 0 {
		b.WriteString("These phrases were measured and must appear in the rewrite exactly as written:\n")
		for _, a := range p.Anchors {
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	if prompts.Overridden(p.ID) {
		b.WriteString("This is their rewrite of Astral's original. The original can be read with " +
			"read_prompt, with the id " + p.ID + "#original.\n")
	}
	b.WriteString("Its current text, between the markers:\n<<<PROMPT\n")
	b.WriteString(prompts.Text(p.ID))
	b.WriteString("\nPROMPT>>>")
	return b.String()
}

// Opening is what the conversation opens with.
func Opening(id string) string {
	if p, ok := prompts.Get(id); ok {
		return "Let's make the " + p.Name + " prompt work better.\n\n" +
			"I have it in front of me, along with every other prompt Astral sends. Tell me what bothers " +
			"you about what it produces, or just say go, and I'll say where I think it falls short and " +
			"write a better version.\n\nWhen you like one, press **Save Prompt** and Astral will use it " +
			"from then on. You can always put the original back."
	}
	return "Paste the prompt you want to improve, and tell me what it is for and which model reads it, " +
		"if you know.\n\nI'll say where it is likely to go wrong and write a better version. I can also " +
		"read any of Astral's own prompts, if you want yours to match them."
}

// firstSentence is the first sentence of a description, for the list.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

// Read answers a read_prompt call.
func Read(id string) string {
	id = strings.TrimSpace(id)
	if kind, ok := strings.CutPrefix(id, "sent."); ok {
		s, found := prompts.SentOf(kind)
		if !found {
			return "Nothing of that kind has been sent since Astral started."
		}
		return "The last " + s.Name + ", exactly as it went to the model at " + s.At.Format("15:04") +
			". The prompts are in it along with what the code puts around them.\n<<<REQUEST\n" +
			s.Text + "\nREQUEST>>>"
	}
	original := strings.HasSuffix(id, "#original")
	id = strings.TrimSuffix(id, "#original")
	p, ok := prompts.Get(id)
	if !ok {
		var ids []string
		for _, q := range prompts.All() {
			ids = append(ids, q.ID)
		}
		return "There is no prompt called " + id + ". The ids are: " + strings.Join(ids, ", ") + "."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s), in %s.\nWhat it is for: %s\n", p.Name, p.ID, p.Group, p.About)
	if k := strings.TrimSpace(p.Keep); k != "" {
		fmt.Fprintf(&b, "What a rewrite must keep: %s\n", k)
	}
	text := prompts.Text(p.ID)
	switch {
	case original:
		b.WriteString("Astral's original text:\n")
		text = p.Default
	case prompts.Overridden(p.ID):
		b.WriteString("Their rewrite, which is what is sent now. Astral's original is " + p.ID + "#original.\n")
	default:
		b.WriteString("Its text, which is Astral's own:\n")
	}
	b.WriteString("<<<PROMPT\n")
	b.WriteString(text)
	b.WriteString("\nPROMPT>>>")
	return b.String()
}

// fenced finds fenced blocks and the word after the opening fence.
var fenced = regexp.MustCompile("(?s)```([A-Za-z]*)[ \t]*\n(.*?)\n[ \t]*```[ \t]*(?:\n|$)")

// Proposal is the prompt a reply proposes: its last block fenced as prompt, or
// failing that its last fenced block of any kind. False when there is none.
// The markers the current text is shown between are taken off, since a model
// that copies the text it was shown copies them too.
func Proposal(reply string) (string, bool) {
	matches := fenced.FindAllStringSubmatch(reply, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if strings.EqualFold(matches[i][1], "prompt") {
			body := Clean(matches[i][2])
			return body, body != ""
		}
	}
	if n := len(matches); n > 0 {
		body := Clean(matches[n-1][2])
		return body, body != ""
	}
	return "", false
}

// Clean takes the <<<PROMPT and PROMPT>>> markers off a proposed prompt. They
// only ever mark where the text shown to the optimizer starts and ends, and a
// saved prompt that still has them sends them to the model on every request.
func Clean(text string) string {
	var keep []string
	for _, line := range strings.Split(text, "\n") {
		switch strings.TrimSpace(line) {
		case "<<<PROMPT", "PROMPT>>>", "<<<REQUEST", "REQUEST>>>":
			continue
		}
		keep = append(keep, line)
	}
	// Some models double an apostrophe, as SQL would escape one.
	return strings.ReplaceAll(strings.TrimSpace(strings.Join(keep, "\n")), "''", "'")
}

// heading finds a markdown heading.
var heading = regexp.MustCompile(`(?m)^#{1,6} `)

// slot finds what a prompt has filled in for it, and anything shaped like it.
var slot = regexp.MustCompile(`\{\{[A-Za-z_]+\}\}|%\[[^\]\s]*\][a-z]?`)

// ProblemsFor is Problems for one of Astral's prompts: the checks against its
// original, and the phrases it protects.
func ProblemsFor(p prompts.Prompt, rewrite string) []string {
	out := Problems(p.Default, rewrite, p.Slots...)
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	have := norm(rewrite)
	for _, a := range p.Anchors {
		if !strings.Contains(have, norm(a)) {
			out = append(out, "It no longer says \""+a+"\", which was measured and is meant to stay.")
		}
	}
	return out
}

// Problems lists what is wrong with a rewrite of original that is worth
// reading before it is saved: a slot the original fills in that was dropped,
// one that was invented and will reach the model as literal text, and dashes,
// which the model writes back.
func Problems(original, rewrite string, fillable ...string) []string {
	var out []string
	has := map[string]bool{}
	for _, s := range slot.FindAllString(original, -1) {
		has[s] = true
	}
	for _, s := range fillable {
		has[s] = true
	}
	seen := map[string]bool{}
	for _, s := range slot.FindAllString(original, -1) {
		if !seen[s] && !strings.Contains(rewrite, s) {
			out = append(out, "The original uses "+s+" and this does not, so it stops saying who is who.")
		}
		seen[s] = true
	}
	for _, s := range slot.FindAllString(rewrite, -1) {
		if !has[s] && !seen[s] {
			out = append(out, s+" is not something Astral fills in, so the model will be sent it as it is.")
		}
		seen[s] = true
	}
	if strings.ContainsAny(rewrite, "\u2014\u2013") {
		out = append(out, "It has em or en dashes, which the model will start writing back.")
	}
	if heading.MatchString(rewrite) && !heading.MatchString(original) {
		out = append(out, "It adds markdown headings, which the model may copy into its replies.")
	}
	if strings.Contains(rewrite, "**") && !strings.Contains(original, "**") {
		out = append(out, "It adds bold, which the model may copy into its replies.")
	}
	for _, m := range []string{"<<<", ">>>"} {
		if strings.Contains(rewrite, m) {
			out = append(out, "It still has a "+m+" marker in it.")
			break
		}
	}
	return out
}

// ReadExtra is read_prompt as a tool for the conversation's tool loop, which
// also carries search and saving to the knowledge base.
func ReadExtra() websearch.Extra {
	return websearch.Extra{
		Tool: Tool,
		Answer: func(ctx context.Context, args json.RawMessage) string {
			return Read(callArg(args))
		},
		Note: func(args json.RawMessage) string {
			id := callArg(args)
			if p, ok := prompts.Get(strings.TrimSuffix(id, "#original")); ok {
				return "Read the " + p.Name + " prompt"
			}
			if kind, ok := strings.CutPrefix(id, "sent."); ok {
				if s, found := prompts.SentOf(kind); found {
					return "Read the last " + s.Name
				}
			}
			return ""
		},
	}
}

// callArg reads the id out of read_prompt's arguments.
func callArg(args json.RawMessage) string {
	var a struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(args, &a)
	return strings.TrimSpace(a.ID)
}
