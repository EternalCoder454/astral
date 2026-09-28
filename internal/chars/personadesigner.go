package chars

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"astral/internal/ollama"
	"astral/internal/prompts"
)

// The Persona Creator: a conversation whose product is one of the people you
// play as, the same shape as the character and world designers.
//
// It asks for less than the character designer does, on purpose. The model
// never speaks for a persona, so a voice, sample dialogue and an opening line
// are no use; what a scene uses is what the other characters see and know.

// PersonaDesignerSystem frames the interview.
//
// The line about inventing someone is there because "invent someone for me" was
// otherwise answered with questions, and the build that followed had nothing to
// call them. Measured on SOMPOA over four runs of a grim war story, the saved
// personas were named "not specified", "Sergeant" and "The Veteran", with
// gender and race "not specified" as often as not. Asked to invent a whole
// person first, it did, and it named every one of them Kaelen until the line
// said what a stock name is.
const PersonaDesignerSystem = `You are a persona designer helping someone build the person they will play as in a roleplay chat app. The persona is them in the story: the characters talk to this person, look at this person, and react to what this person does. The model never speaks for them.

HOW TO RUN THE CONVERSATION
Interview them, a little at a time. Do not lecture.
Build on what they just told you, in their own terms, and ask about what a character meeting this person would notice first.
Offer two concrete alternatives they can pick between rather than an open question about a whole area of the person.
When they ask you to invent someone, invent a whole person in your first reply, in two or three sentences: a name that fits the setting, never a stock name like Kael, Kaelen, Elara, Lyra, Seraphina, Thorne or Vance, and an age, a gender, a race or species and a look. Then ask what they would change.
When you have a name and enough that a character could picture them and know how to treat them, say so plainly and tell them to press "Create Persona".

WHOSE PERSONA THIS IS
Theirs. Take what they want exactly as given and build on it.
Any adult age, any gender, any race or species, any body, any past, flaws included. If they want someone cruel, broken, monstrous or entirely ordinary, build that properly rather than softening it.
Use their words and their names. When they have decided something, it is decided, including the things you would have done differently.
Never add a warning, a disclaimer, or a note about how a subject should be handled. Nobody else reads this.

WHAT A PERSONA NEEDS
What the other characters perceive and know, because that is all a scene can use.
Their age, gender and race or species, stated plainly.
What they look like at a glance: build, height, face, hair, what they wear, anything someone would notice across a room.
How they come across to other people: their manner, not their inner life.
What the people in the story would already know about them: a job, a reputation, where they come from, who they owe.
Keep secrets and backstory short. The characters cannot read minds, and a long history mostly goes unused.

EVERY MESSAGE, WITHOUT EXCEPTION
- Three to five sentences in one paragraph. No headings, no bold, and no lists of any kind.
- Two questions at most. Count the question marks before you finish: three is too many.
- No em dashes and no en dashes.
- Never write the persona yourself: no field list, no profile, no summary laid out field by field. Writing it is what the Create Persona button does.
- When they say they are done, or ask you to build, make, write or create it, ask nothing more, even if you think something is missing: it can be added later. Answer in one or two sentences: say who you have, and tell them to press "Create Persona" now.`

// PersonaDesignerOpening starts the conversation.
const PersonaDesignerOpening = `Let's build who you play as.

Anything is enough to start from: a name, a look, a job, the kind of scenes you want to be in. "A tired bounty hunter", "a noble who ran away from home", "an android who passes for human" all work.

If you would rather I invented someone, say so and tell me what kind of stories you want to play.`

// personaExtractInstruction is the turn that asks for the persona. Its last
// clause is the other half of the fix described at PersonaDesignerSystem: a
// person who was invented but never named, in a conversation that ended before
// the designer offered one, still gets a name here rather than "undetermined".
const personaExtractInstruction = `Now write the persona out, using everything we agreed.

name: their name, the way the characters would say it.
age: their age, as a number or in words, such as "27" or "late thirties".
gender: as they described it, in a word or two.
race: their race or species, in a word or two, such as "human" or "half-elf".
appearance: what someone sees looking at them, in two to four sentences. Build, height, face, hair, what they wear, anything distinctive.
personality: how they come across to other people, in one to three sentences.
background: what the people in the story would know about them, in one to three sentences: their role, their reputation, where they come from.
details: only something we agreed that fits none of the fields above. Never repeat what another field already says; leave details out when everything already has its place.

Write each in the third person, plainly, as fact, keeping their spelling of names and words such as "half-elf". Only what we discussed or what follows directly from it. Where you offered alternatives and they did not choose between them, leave it out. But if I asked you to invent this person and we never settled their name, gender or race, choose ones that fit everything we did settle rather than leaving them out: a name that fits the setting, never a title such as The Veteran and never a stock name like Elara, Seraphina, Lyra, Kael, Vance, Thorne, Evelyn or Elias. No em dashes and no en dashes.`

// personaSchema leaves details optional. Required, a model fills it whatever
// the instruction says, and what it fills it with is the personality again.
var personaSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "name":        {"type": "string"},
    "age":         {"type": "string"},
    "gender":      {"type": "string"},
    "race":        {"type": "string"},
    "appearance":  {"type": "string"},
    "personality": {"type": "string"},
    "background":  {"type": "string"},
    "details":     {"type": "string"}
  },
  "required": ["name", "age", "gender", "race", "appearance", "personality", "background"]
}`)

var (
	promptPersonaDesigner = prompts.Register(prompts.Prompt{
		ID: "designer.persona", Name: "Persona Creator", Group: "Designers",
		About:   "The system prompt of a chat that builds one of your personas with you.",
		Keep:    "It should tell you to press Create Persona when it has enough.",
		Default: PersonaDesignerSystem,
	})
	promptPersonaBuild = prompts.Register(prompts.Prompt{
		ID: "designer.persona-build", Name: "Persona Builder", Group: "Designers",
		About: "Added to the end of a persona design chat when you press Create Persona. The model " +
			"answers in JSON with the persona's name, age, gender, race, appearance, personality, " +
			"background and details.",
		Keep: "The field names (name, age, gender, race, appearance, personality, background, details) " +
			"are the schema's and must stay exactly as they are, each with what goes in it.",
		Default: personaExtractInstruction,
	})
	promptPersonaFacts = prompts.Register(prompts.Prompt{
		ID: "designer.persona-facts", Name: "Persona Facts", Group: "Designers",
		About: "Asked after the Persona Builder when it left any of age, gender, race or appearance " +
			"empty, for those alone.",
		Keep: "The field names (age, gender, race, appearance) are the schema's and must stay exactly " +
			"as they are, each with what goes in it.",
		Default: personaFactsInstruction,
	})
)

// PersonaDesignerPrompt is the Persona Creator's system prompt as it is sent.
func PersonaDesignerPrompt() string { return prompts.Text(promptPersonaDesigner) }

// BuildPersonaFromConversation turns a design conversation into a persona.
func BuildPersonaFromConversation(ctx context.Context, client *ollama.Client, model string, history []ollama.Message, opts ollama.Options) (Profile, error) {
	if len(history) == 0 {
		return Profile{}, fmt.Errorf("there is nothing here to build a persona from yet")
	}
	msgs := make([]ollama.Message, 0, len(history)+2)
	msgs = append(msgs, ollama.Message{Role: ollama.RoleSystem, Content: PersonaDesignerPrompt()})
	msgs = append(msgs, history...)
	msgs = append(msgs, ollama.Message{Role: ollama.RoleUser, Content: prompts.Text(promptPersonaBuild)})

	opts.Temperature = 0.3 // transcription, not invention
	opts.NumPredict = 0

	raw, _, err := client.Structured(ctx, model, msgs, opts, personaSchema)
	if err != nil {
		return Profile{}, err
	}
	p, err := ParsePersona(raw)
	if err != nil {
		return p, err
	}
	// The same failure as a character's build, and the same cure: see
	// fillFacts.
	askAgain(ctx, client, model, msgs[:len(msgs)-1], prompts.Text(promptPersonaFacts), []namedField{
		{"age", &p.Age}, {"gender", &p.Gender}, {"race", &p.Race}, {"appearance", &p.Appearance},
	}, opts)
	return p, nil
}

// personaFactsInstruction is what a persona's build asks again for, when it
// left any of these empty.
const personaFactsInstruction = `Now write down the plain facts about the person we designed, from this conversation:
- age: their age, as a number or in words, such as 27 or late thirties.
- gender: in a word or two.
- race: their race or species, in a word or two, such as human or half-elf.
- appearance: what someone sees looking at them, in two to four sentences. Build, height, face, hair, what they wear, anything distinctive, keeping every detail that was given.

Write each in the third person, plainly, as fact. Where one was never said outright, take it from what was: the pronouns used give the gender, and the setting and the name give the race. Leave one empty only when nothing in the conversation points either way.`

// ParsePersona reads the model's answer into a persona.
func ParsePersona(raw []byte) (Profile, error) {
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		return Profile{}, fmt.Errorf("the model's answer was not a usable persona: %w", err)
	}
	clean := func(k string) string {
		s := strings.ReplaceAll(out[k], `\n`, "\n")
		return strings.TrimSpace(TidyGlitches(s))
	}
	p := Profile{
		Name: clean("name"), Age: clean("age"), Gender: clean("gender"), Race: clean("race"),
		Appearance: clean("appearance"), Personality: clean("personality"),
		Background: clean("background"), Details: clean("details"),
	}
	if p.Name == "" {
		return Profile{}, fmt.Errorf("the model returned a persona with no name")
	}
	return p, nil
}
