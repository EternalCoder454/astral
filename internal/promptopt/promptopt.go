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

WHAT YOU MUST NOT DO
Do not add restrictions, disclaimers, refusals, content warnings or safety rules. Astral runs on the person's own machine, and what its prompts allow is theirs to decide. Where a prompt already removes restrictions, keep that, and make it work better if you can.
Do not change what the prompt is for. A better prompt that does a different job is a worse prompt.
Do not drop anything listed under what a rewrite must keep. The code around the prompt depends on it.
Never give part of a prompt with "the rest unchanged". What you give is saved exactly as written.

HOW TO GIVE A REWRITE
Put the complete prompt in one fenced block that opens with ` + "```prompt" + ` on its own line and closes with ` + "```" + `. Nothing else goes inside it: no notes, no markers, no placeholders of your own. After the block, list what you changed and why, one short line each.

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

// maxRounds bounds how many times one reply may read prompts before it has to
// answer with what it has.
const maxRounds = 4

// Run writes one reply, reading prompts when the model asks to. onRead is told
// the name of each prompt read, so the window can say so while it happens, and
// onDiscard that the text streamed so far was a preamble to a read rather than
// the answer.
//
// A model without tool calling ignores the tool and answers from the list and
// the prompt it was given, which is most of what it needs.
func Run(ctx context.Context, client *ollama.Client, model string, msgs []ollama.Message, opts ollama.Options,
	think *bool, onDelta func(ollama.Delta), onRead func(name string), onDiscard func()) (ollama.Message, ollama.Stats, error) {
	conv := append([]ollama.Message(nil), msgs...)
	for i := 0; ; i++ {
		tools := []ollama.Tool{Tool}
		if i >= maxRounds {
			tools = nil
		}
		streamed := false
		stream := func(d ollama.Delta) {
			if d.Content != "" {
				streamed = true
			}
			if onDelta != nil {
				onDelta(d)
			}
		}
		msg, stats, err := client.ChatTools(ctx, model, conv, opts, think, tools, stream)
		if err != nil || len(msg.ToolCalls) == 0 || tools == nil {
			return msg, stats, err
		}
		if streamed && onDiscard != nil {
			onDiscard()
		}
		conv = append(conv, msg)
		for _, call := range msg.ToolCalls {
			id := callID(call)
			if onRead != nil {
				name := id
				if p, ok := prompts.Get(strings.TrimSuffix(id, "#original")); ok {
					name = p.Name
				}
				onRead(name)
			}
			conv = append(conv, ollama.Message{Role: ollama.RoleTool, ToolName: ToolName, Content: Read(id)})
		}
		if ctx.Err() != nil {
			return ollama.Message{}, ollama.Stats{}, ctx.Err()
		}
	}
}

// callID reads the id out of a read_prompt call, including from a model that
// sends its arguments as a JSON string rather than an object.
func callID(call ollama.ToolCall) string {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
		var inner string
		if json.Unmarshal(call.Function.Arguments, &inner) == nil {
			_ = json.Unmarshal([]byte(inner), &args)
		}
	}
	return strings.TrimSpace(args.ID)
}

// fenced finds fenced blocks and the word after the opening fence.
var fenced = regexp.MustCompile("(?s)```([A-Za-z]*)[ \t]*\n(.*?)\n[ \t]*```[ \t]*(?:\n|$)")

// Proposal is the prompt a reply proposes: its last block fenced as prompt, or
// failing that its last fenced block of any kind. False when there is none.
func Proposal(reply string) (string, bool) {
	matches := fenced.FindAllStringSubmatch(reply, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if strings.EqualFold(matches[i][1], "prompt") {
			return strings.TrimSpace(matches[i][2]), strings.TrimSpace(matches[i][2]) != ""
		}
	}
	if n := len(matches); n > 0 {
		body := strings.TrimSpace(matches[n-1][2])
		return body, body != ""
	}
	return "", false
}
