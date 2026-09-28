package websearch

import "astral/internal/prompts"

// promptGuidance is the web search block, registered so the Prompt Optimizer
// can read and rewrite it.
var promptGuidance = prompts.Register(prompts.Prompt{
	ID: "chat.search", Name: "Web Search Guidance", Group: "Conversation",
	About: "Added to the end of General Chat's and the designers' system prompts while web search is on, " +
		"to teach the model when and how to search.",
	Keep:    "The tool names " + ToolName + " and " + OpenToolName + " are what the model calls, so they must stay.",
	Default: Guidance,
})

// GuidanceText is the web search block as it is sent.
func GuidanceText() string { return prompts.Text(promptGuidance) }
