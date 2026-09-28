package world

import "astral/internal/prompts"

// The prompts this package sends, registered so the Prompt Optimizer can read
// and rewrite them. See the chars package's prompts.go for why they are used
// through prompts.Text rather than their constants.
var (
	promptDesigner = prompts.Register(prompts.Prompt{
		ID: "designer.world", Name: "World Designer", Group: "Designers",
		About:   "The system prompt of a chat that designs a world with you.",
		Keep:    "It must not write the world or JSON itself, and should tell you to press Create World when it has enough.",
		Default: DesignerSystem,
	})
	promptBuild = prompts.Register(prompts.Prompt{
		ID: "designer.world-build", Name: "World Builder", Group: "Designers",
		About: "Added to the end of a world design chat when you press Create World. The model answers in " +
			"JSON with a name, a description, rules and lorebook entries.",
		Keep: "The field names (name, description, rules, entries, and each entry's keys and content) are " +
			"the schema's and must stay exactly as they are.",
		Default: extractInstruction,
	})
	promptReviseWorld = prompts.Register(prompts.Prompt{
		ID: "designer.world-revise", Name: "World Reviser", Group: "Designers",
		About: "The system prompt when you ask a designer to rewrite a world you already have. The world " +
			"as it stands is added after it.",
		Keep:    "It should tell you to press Save World when it has enough.",
		Default: reviseWorldSystem,
	})
	promptLearn = prompts.Register(prompts.Prompt{
		ID: "worlds.learn", Name: "Lorebook Learning", Group: "Worlds",
		About: "Asked of the background model every few turns of a scene set in a world: it reads what " +
			"just happened and proposes lorebook entries for what the scene established.",
		Keep:    "The reply is JSON shaped by a fixed schema, so keep what it says about each field.",
		Default: learnSystem,
	})
	promptFromText = prompts.Register(prompts.Prompt{
		ID: "worlds.from-text", Name: "Lorebook From a Document", Group: "Worlds",
		About: "Sent when you paste a document about a setting into a world: it turns the text into " +
			"lorebook entries.",
		Keep:    "The reply is JSON shaped by a fixed schema, so keep what it says about each field.",
		Default: fromTextSystem,
	})
)

// DesignerPrompt is the World Designer's system prompt as it is sent.
func DesignerPrompt() string { return prompts.Text(promptDesigner) }
