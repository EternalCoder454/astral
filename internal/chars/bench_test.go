package chars

import (
	"strings"
	"testing"

	"astral/internal/ollama"
)

func benchScene() (Character, Scene) {
	seed := "She turned the map face down before I could see the coastline. The dividers stopped. "
	var long strings.Builder
	for long.Len() < 900 {
		long.WriteString(seed)
	}
	c := Character{
		Name: "Vesper Quill", Description: long.String(), Personality: "wry, guarded",
		Scenario:   "The map room, past midnight.",
		MesExample: "<START>\n{{user}}: Hello.\n{{char}}: *She did not look up.* \"Is it.\"",
	}
	p := Persona{Name: "Christian", Description: "A courier.", Style: DefaultStyle()}

	var hist []ollama.Message
	for i := 0; i < 40; i++ {
		role := ollama.RoleUser
		if i%2 == 1 {
			role = ollama.RoleAssistant
		}
		hist = append(hist, ollama.Message{Role: role,
			Content: `*She set the dividers down, and did not pick them up again.* "The tide came in early."`})
	}
	return c, Scene{Persona: p, History: hist,
		Lore:   "## Kestrel Bay\nA port city three days north.\n",
		Recap:  "Vesper admitted she was expelled from the Guild.",
		Budget: Plan(8192, 0, 4000)}
}

// This runs once per turn, on the UI thread, before the request goes out. It
// is nowhere near the cost of a generation, but it is in the way of the first
// token, so it is worth knowing.
func BenchmarkBuildMessages(b *testing.B) {
	c, sc := benchScene()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = BuildMessages(c, sc)
	}
}

// The system prompt is built to measure its length for the budget, and then
// built again inside BuildMessages. Whether that matters is what this answers.
func BenchmarkBuildSystem(b *testing.B) {
	c, sc := benchScene()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = BuildSystem(c, sc.Persona)
	}
}

// Runs once per turn over the recent transcript.
func BenchmarkNarrationDrifted(b *testing.B) {
	_, sc := benchScene()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = NarrationDrifted(sc.History)
	}
}
