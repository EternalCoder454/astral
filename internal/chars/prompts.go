package chars

import "astral/internal/prompts"

// The prompts this package sends, registered so the Prompt Optimizer can read
// and rewrite them. Each is used through prompts.Text, never through its
// constant, so a rewrite applies everywhere it is sent. The constants stay
// where they are, beside the reasons for what they say.

const keepFormat = "The two-kind rule for formatting (speech in \"double quotes\", everything else in " +
	"*single asterisks*) is what the transcript is drawn from, so it must stay. "

var (
	promptFraming = prompts.Register(prompts.Prompt{
		ID: "scene.framing", Name: "Scene Framing", Group: "Scenes",
		About: "The opening of every one-on-one scene's system prompt. After it come the writing style " +
			"(under HOW TO WRITE IT), the Scene Rules prompt, and then the character's card, the world, " +
			"the lorebook and the recap.",
		Keep:    keepFormat + "{{char}} becomes the character's name and {{user}} yours.",
		Default: framingStructure,
	})
	promptFramingClose = prompts.Register(prompts.Prompt{
		ID: "scene.close", Name: "Scene Rules", Group: "Scenes",
		About: "A line sent after the writing style in every scene, one-on-one or group, so a style " +
			"cannot talk its way past it.",
		Default: framingClose,
	})
	promptGroup = prompts.Register(prompts.Prompt{
		ID: "scene.group", Name: "Group Scene Framing", Group: "Scenes",
		About: "The opening of the system prompt for a scene with several characters. The cast, the " +
			"writing style and the Scene Rules follow it.",
		Keep: keepFormat + "%[1]s and %[2]s become the first two characters' names and %[3]s yours. " +
			"Beats must start with the speaker's name and a colon, because that is how a reply is split " +
			"between the characters.",
		Default: groupStructure,
	})
	promptFormat = prompts.Register(prompts.Prompt{
		ID: "scene.format", Name: "Format Reminder", Group: "Scenes",
		About: "Restated at the very end of every scene's request, after the transcript, because by then " +
			"the transcript outweighs the system prompt.",
		Keep:    keepFormat,
		Default: anchorFormat,
	})
	promptFormatFirm = prompts.Register(prompts.Prompt{
		ID: "scene.format-firm", Name: "Format Correction", Group: "Scenes",
		About: "Sent in place of the Format Reminder once the recent replies have stopped marking their " +
			"narration, to pull the scene back.",
		Keep:    keepFormat,
		Default: anchorFormatFirm,
	})

	promptAssistant = prompts.Register(prompts.Prompt{
		ID: "chat.assistant", Name: "General Chat", Group: "Conversation",
		About: "The system prompt of a plain conversation with the model. Standing rules, knowledge " +
			"and web search guidance are added after it.",
		Default: AssistantSystem,
	})

	promptDesigner = prompts.Register(prompts.Prompt{
		ID: "designer.character", Name: "Character Designer", Group: "Designers",
		About: "The system prompt of a design chat, where the model interviews you about a new character.",
		Keep: "It must not write the card or JSON itself: that is the Character Builder's job, when you " +
			"press Create Character, which is the button it should tell you to press.",
		Default: DesignerSystem,
	})
	promptBuild = prompts.Register(prompts.Prompt{
		ID: "designer.character-build", Name: "Character Builder", Group: "Designers",
		About: "Added to the end of a design chat when you press Create Character. The model answers in " +
			"JSON shaped by a fixed schema, so this says what goes in each field.",
		Keep: "The field names (name, description, personality, appearance, speech, scenario, first_mes, " +
			"mes_example, tags) are the schema's and must stay exactly as they are.",
		Default: extractInstruction,
	})
	promptReviseCharacter = prompts.Register(prompts.Prompt{
		ID: "designer.character-revise", Name: "Character Reviser", Group: "Designers",
		About: "The system prompt when you ask a designer to rewrite a character you already have. The " +
			"card as it stands is added after it.",
		Keep:    "It should tell you to press Save Character when it has enough.",
		Default: reviseCharacterSystem,
	})
	promptStyleDesigner = prompts.Register(prompts.Prompt{
		ID: "designer.style", Name: "Style Designer", Group: "Designers",
		About:   "The system prompt of a chat that designs a writing style with you.",
		Keep:    "It should tell you to press Create Style when it has enough.",
		Default: StyleDesignerSystem,
	})
	promptStyleBuild = prompts.Register(prompts.Prompt{
		ID: "designer.style-build", Name: "Style Builder", Group: "Designers",
		About: "Added to the end of a style design chat when you press Create Style. The model answers in " +
			"JSON with a name and the rules.",
		Default: styleExtractInstruction,
	})
	promptReviseStyle = prompts.Register(prompts.Prompt{
		ID: "designer.style-revise", Name: "Style Reviser", Group: "Designers",
		About: "The system prompt when you ask a designer to rewrite a writing style you already have. The " +
			"style as it stands is added after it.",
		Keep:    "It should tell you to press Save Style when it has enough.",
		Default: reviseStyleSystem,
	})

	promptSeeingDesign = prompts.Register(prompts.Prompt{
		ID: "pictures.design", Name: "Reading a Picture for a Design", Group: "Pictures",
		About: "Sent to a model that can see, with a picture from a design chat, when the chat's own model " +
			"cannot. Its description goes into the conversation in place of the picture.",
		Default: seeingForDesign,
	})
	promptSeeingChat = prompts.Register(prompts.Prompt{
		ID: "pictures.chat", Name: "Reading a Picture for a Chat", Group: "Pictures",
		About:   "The same, for a picture sent into a plain conversation.",
		Default: seeingForChat,
	})

	promptRecord = prompts.Register(prompts.Prompt{
		ID: "memory.scene", Name: "Scene Record", Group: "Memory",
		About: "Asked of the background model when a scene outgrows its context: it folds the oldest turns " +
			"into a running record that is sent in their place.",
		Keep: "The record's section headings are looked for in the reply, and the reply is cut at the first " +
			"one that repeats, so keep them word for word.",
		Default: compactSystem,
	})
	promptRecordPlain = prompts.Register(prompts.Prompt{
		ID: "memory.chat", Name: "Conversation Record", Group: "Memory",
		About:   "The same for a plain conversation, which needs what was decided rather than who is where.",
		Keep:    "Keep its section headings word for word, for the same reason as the Scene Record's.",
		Default: compactPlainSystem,
	})
	promptRecordAgain = prompts.Register(prompts.Prompt{
		ID: "memory.again", Name: "Record, Second Try", Group: "Memory",
		About:   "Added to either record prompt when the first attempt came back far too long.",
		Default: recordAgain,
	})
)

// DesignerPrompt is the Character Designer's system prompt as it is sent.
func DesignerPrompt() string { return prompts.Text(promptDesigner) }

// StyleDesignerPrompt is the Style Designer's system prompt as it is sent.
func StyleDesignerPrompt() string { return prompts.Text(promptStyleDesigner) }

// AssistantPrompt is General Chat's system prompt as it is sent.
func AssistantPrompt() string { return prompts.Text(promptAssistant) }
