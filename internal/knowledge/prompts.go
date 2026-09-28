package knowledge

import "astral/internal/prompts"

// promptNotes is the study notes prompt, registered so the Prompt Optimizer can
// read and rewrite it.
var promptNotes = prompts.Register(prompts.Prompt{
	ID: "knowledge.notes", Name: "Study Notes", Group: "Knowledge",
	About: "Sent when you ask Astral to study a topic: the pages it found are given after it, and the " +
		"notes it writes are saved to Knowledge.",
	Keep:    "Sources are cited by number in square brackets, like [1], because the pages are numbered that way.",
	Default: notesSystem,
})
