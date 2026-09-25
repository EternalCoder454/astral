package chars

import (
	"fmt"
	"strings"
	"testing"

	"astral/internal/ollama"
)

func bpara(n int) string {
	s := "She turned the map face down before I could see the coastline, and said nothing about it. " +
		"The dividers stopped. It was the third delay this month and everyone in the room knew it. "
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(s)
	}
	return b.String()[:n]
}

// The regression this file exists for, stated as the scene that caused it: a
// rich card, a full lorebook, a full recap and a long transcript, all at once.
// Before the budget was planned this built a prompt of about 9,750 tokens
// against a window of 8,192, and the server silently dropped the front of it —
// which is the framing that says to use asterisks, and the writing style.
func TestWorstCaseNowFits(t *testing.T) {
	c := Character{
		Name: "Vesper Quill", Description: bpara(900), Personality: bpara(300),
		Scenario: bpara(400), MesExample: "<START>\nUser: Hello.\nVesper Quill: " + bpara(600),
		Instructions: bpara(200),
	}
	p := Persona{Name: "Christian", Description: bpara(300),
		GlobalInstructions: bpara(200), Style: DefaultStyle()}

	var hist []ollama.Message
	for i := 0; i < 60; i++ {
		role := ollama.RoleUser
		if i%2 == 1 {
			role = ollama.RoleAssistant
		}
		hist = append(hist, ollama.Message{Role: role, Content: bpara(800)})
	}
	budget := Plan(8192, 0, len(BuildSystem(c, p)))
	msgs := BuildMessages(c, Scene{Persona: p, Lore: bpara(3000), Recap: bpara(2400),
		History: hist, Budget: budget})

	total := 0
	for _, m := range msgs {
		total += len(m.Content)
	}
	optimistic, realistic := total/4, int(float64(total)/3.5)
	fmt.Printf("\n  prompt chars      %d\n", total)
	fmt.Printf("  ~tokens at 4.0    %d\n", optimistic)
	fmt.Printf("  ~tokens at 3.5    %d\n", realistic)
	fmt.Printf("  reply reserved    %d\n", DefaultReplyTokens)
	fmt.Printf("  window            8192\n")
	fmt.Printf("  headroom at 3.5   %d tokens\n\n", 8192-realistic-DefaultReplyTokens)

	if realistic+DefaultReplyTokens > 8192 {
		t.Errorf("prompt plus reply is %d tokens against an 8192 window",
			realistic+DefaultReplyTokens)
	}
}
