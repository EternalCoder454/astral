package chars

import (
	"strings"
	"testing"

	"astral/internal/ollama"
)

func fillTo(n int, seed string) string {
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(seed)
	}
	return b.String()[:n]
}

// flattenPrompt is how the prompt reaches the model: the messages run together
// through the chat template. The character-level prefix is a fair proxy for
// the token-level one Ollama's cache actually matches on.
func flattenPrompt(msgs []ollama.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString("<|" + m.Role + "|>\n" + m.Content + "\n")
	}
	return b.String()
}

func sharedPrefix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// The regression this guards: lore is matched against what was recently said,
// so it changes whenever the conversation moves on. Placed before the
// transcript it invalidated the server's cached prefix on most turns, and the
// whole scene was re-read from scratch. Measured at 12% reusable before the
// reorder.
func TestLoreChangeKeepsThePrefixCacheable(t *testing.T) {
	c := Character{Name: "Vesper", Description: fillTo(900, "A cartographer of places that have not happened yet. ")}
	p := Persona{Name: "Christian", Style: DefaultStyle()}

	var hist []ollama.Message
	for i := 0; i < 20; i++ {
		role := ollama.RoleUser
		if i%2 == 1 {
			role = ollama.RoleAssistant
		}
		hist = append(hist, ollama.Message{Role: role, Content: fillTo(700, "The tide came in early and the ferry did not. ")})
	}
	recap := fillTo(2000, "Vesper admitted she was expelled. ")
	// An explicit, generous budget, so this measures lore placement and not
	// trimming. Trimming drops turns off the front and moves everything after
	// them, which invalidates the prefix too — but that is compaction's job to
	// prevent, and TestCompactionRunsBeforeTrimming is where it is checked.
	budget := Plan(32768, 0, 4000)

	before := flattenPrompt(BuildMessages(c, Scene{Persona: p, Recap: recap, Budget: budget,
		Lore: "## Kestrel Bay\nA port city three days north.\n", History: hist}))

	// One more exchange, and the subject has changed, so different lore matches.
	next := append(append([]ollama.Message{}, hist...),
		ollama.Message{Role: ollama.RoleUser, Content: "Tell me about the Guild."},
		ollama.Message{Role: ollama.RoleAssistant, Content: fillTo(700, "She rubbed at her wrist. ")})
	after := flattenPrompt(BuildMessages(c, Scene{Persona: p, Recap: recap, Budget: budget,
		Lore: "## Cartographers' Guild\nForbids charting east of the Sever.\n", History: next}))

	reusable := float64(sharedPrefix(before, after)) / float64(len(after))
	t.Logf("reusable prefix across a lore change: %.1f%%", 100*reusable)
	if reusable < 0.85 {
		t.Errorf("only %.1f%% of the prompt is reusable after a lore change, want at least 85%%: "+
			"something volatile has moved back in front of the transcript", 100*reusable)
	}
}

// The transcript growing must not disturb anything before it.
func TestAppendingATurnKeepsThePrefix(t *testing.T) {
	c := Character{Name: "Vesper", Description: "A cartographer."}
	p := Persona{Name: "Christian", Style: DefaultStyle()}
	hist := []ollama.Message{
		{Role: ollama.RoleUser, Content: "Hello."},
		{Role: ollama.RoleAssistant, Content: `*She did not look up.* "You're late."`},
	}
	lore := "## Kestrel Bay\nA port city.\n"

	before := flattenPrompt(BuildMessages(c, Scene{Persona: p, Lore: lore, History: hist}))
	next := append(append([]ollama.Message{}, hist...),
		ollama.Message{Role: ollama.RoleUser, Content: "I sat."})
	after := flattenPrompt(BuildMessages(c, Scene{Persona: p, Lore: lore, History: next}))

	// Everything up to the new turn's own role marker must match byte for
	// byte. The marker is the boundary, not the text after it: that is the
	// first byte the two prompts can legitimately disagree on.
	want := strings.Index(after, "<|user|>\nI sat.")
	if got := sharedPrefix(before, after); got < want {
		t.Errorf("prefix diverged at %d, %d bytes before the new turn begins at %d",
			got, want-got, want)
	}
}

// Lore has to reach the model wherever it is placed. Moving it for the cache's
// sake is only acceptable if it is still there.
func TestLoreStillReachesTheModel(t *testing.T) {
	msgs := BuildMessages(Character{Name: "Vesper"}, Scene{
		Persona: Persona{Name: "Wren"},
		Lore:    "## Kestrel Bay\nA port city three days north.",
		History: []ollama.Message{{Role: ollama.RoleUser, Content: "Where are we?"}},
	})
	var found bool
	for _, m := range msgs {
		if strings.Contains(m.Content, "A port city three days north") {
			found = true
		}
	}
	if !found {
		t.Fatal("lore is not in the prompt at all")
	}
	// And it must land after the transcript, or the reorder did nothing.
	loreAt, histAt := -1, -1
	for i, m := range msgs {
		if strings.Contains(m.Content, "A port city three days north") {
			loreAt = i
		}
		if m.Content == "Where are we?" {
			histAt = i
		}
	}
	if loreAt < histAt {
		t.Errorf("lore at %d is still before the transcript at %d", loreAt, histAt)
	}
}

// The complaint this answers: changing the writing style changed very little,
// because the strongest style signal in the context is twenty of the model's
// own replies written in the old one.
func TestStyleChangeTellsTheModelNotToImitate(t *testing.T) {
	c := Character{Name: "Vesper"}
	sc := Scene{
		Persona:      Persona{Name: "Wren", Style: WritingStyle{Name: "Terse", Instructions: "Length: One paragraph."}},
		History:      []ollama.Message{{Role: ollama.RoleUser, Content: "Hi"}},
		StyleChanged: true,
	}
	last := BuildMessages(c, sc)
	anchor := last[len(last)-1].Content
	if !strings.Contains(anchor, "do not imitate them") {
		t.Errorf("a changed style does not tell the model to stop imitating the transcript:\n%s", anchor)
	}
	if !strings.Contains(anchor, "Length: One paragraph.") {
		t.Errorf("the new style is not restated at the end of the context:\n%s", anchor)
	}
}

// The style must be stated at the end whether or not it changed: the end is
// the position a model actually obeys.
func TestStyleIsRestatedAtTheEnd(t *testing.T) {
	msgs := BuildMessages(Character{Name: "Vesper"}, Scene{
		Persona: Persona{Name: "Wren", Style: WritingStyle{Name: "Terse", Instructions: "Length: One paragraph."}},
		History: []ollama.Message{{Role: ollama.RoleUser, Content: "Hi"}},
	})
	if a := msgs[len(msgs)-1].Content; !strings.Contains(a, "Length: One paragraph.") {
		t.Errorf("style missing from the anchor:\n%s", a)
	}
}

func TestNarrationDrifted(t *testing.T) {
	marked := ollama.Message{Role: ollama.RoleAssistant,
		Content: `*She set the cup down harder than she meant to, and hated that he noticed it.* "It's fine."`}
	unmarked := ollama.Message{Role: ollama.RoleAssistant,
		Content: `She set the cup down harder than she meant to, and hated that he noticed. "It's fine."`}
	allDialogue := ollama.Message{Role: ollama.RoleAssistant, Content: `"No. Ask me again when the tide is out."`}
	user := ollama.Message{Role: ollama.RoleUser, Content: "Some unmarked prose from the person playing, which is not the model's doing at all."}

	tests := []struct {
		name string
		hist []ollama.Message
		want bool
	}{
		{"marked throughout", []ollama.Message{marked, marked, marked}, false},
		{"unmarked throughout", []ollama.Message{unmarked, unmarked, unmarked}, true},
		{"one slip among three is not drift", []ollama.Message{marked, unmarked, marked}, false},
		// Two of three is the shape a real scene drifts in: bad replies
		// interleaved with good ones, never three bad in a row.
		{"two of three is drift", []ollama.Message{unmarked, marked, unmarked}, true},
		// A reply that is entirely dialogue is correct and has no asterisks.
		// Nagging about it would leave the firmer wording switched on forever.
		{"all dialogue is not drift", []ollama.Message{allDialogue, allDialogue, allDialogue}, false},
		{"the user's own prose is not counted", []ollama.Message{user, user, marked}, false},
		{"empty history", nil, false},
	}
	for _, tt := range tests {
		if got := NarrationDrifted(tt.hist); got != tt.want {
			t.Errorf("%s: NarrationDrifted = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestDriftEscalatesTheFormatRule(t *testing.T) {
	c := Character{Name: "Vesper"}
	base := Scene{Persona: Persona{Name: "Wren"}, History: []ollama.Message{{Role: ollama.RoleUser, Content: "Hi"}}}
	calm := BuildMessages(c, base)
	base.NarrationDrifted = true
	firm := BuildMessages(c, base)

	if strings.Contains(calm[len(calm)-1].Content, "have been getting this wrong") {
		t.Error("the firm wording is used when nothing has drifted")
	}
	if !strings.Contains(firm[len(firm)-1].Content, "have been getting this wrong") {
		t.Error("drift does not escalate the format rule")
	}
}

func TestRestorePrefill(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{
			// The model continued the open span: odd count, so restoring it
			// is what makes the markup balance.
			"continued the prefill",
			`She did not look up.* "You're late."`,
			`*She did not look up.* "You're late."`,
		},
		{
			// The template closed the prefill off and the model wrote a fresh,
			// already-marked reply. Prepending here would break it.
			"template closed the prefill",
			`*She did not look up.* "You're late."`,
			`*She did not look up.* "You're late."`,
		},
		{"empty", "", ""},
		{"no markup at all", `She did not look up.`, `She did not look up.`},
		{
			"several spans, continued",
			`She did not look up.* "You're late." *A pin went in.* "Sit."`,
			`*She did not look up.* "You're late." *A pin went in.* "Sit."`,
		},
	}
	for _, tt := range tests {
		if got := RestorePrefill(tt.in); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func TestDirectionReachesTheAnchor(t *testing.T) {
	c := Character{Name: "Vesper"}
	sc := Scene{
		Persona:   Persona{Name: "Wren"},
		History:   []ollama.Message{{Role: ollama.RoleUser, Content: "Hi"}},
		Direction: "{{char}} is about to realise {{user}} lied about the manifest.",
	}
	msgs := BuildMessages(c, sc)
	anchor := msgs[len(msgs)-1].Content

	// Placeholders expand here like everywhere else: a direction is written in
	// the same box as everything else and should behave the same.
	if !strings.Contains(anchor, "Vesper is about to realise Wren lied") {
		t.Errorf("direction missing or unsubstituted:\n%s", anchor)
	}
	// The two failure modes the wording exists to prevent: doing nothing, and
	// quoting the instruction back as prose instead of playing it.
	for _, want := range []string{"must take a visible step", "Do not state the direction itself"} {
		if !strings.Contains(anchor, want) {
			t.Errorf("direction framing missing %q:\n%s", want, anchor)
		}
	}
	// It must come after the standing instructions, which is the position that
	// makes it the last thing read.
	sc.Persona.GlobalInstructions = "Keep replies short."
	anchor = BuildMessages(c, sc)[len(BuildMessages(c, sc))-1].Content
	if strings.Index(anchor, "DIRECTION") < strings.Index(anchor, "Keep replies short.") {
		t.Error("the direction is placed before the standing instructions")
	}
}

func TestNoDirectionSaysNothing(t *testing.T) {
	msgs := BuildMessages(Character{Name: "Vesper"}, Scene{
		Persona: Persona{Name: "Wren"},
		History: []ollama.Message{{Role: ollama.RoleUser, Content: "Hi"}},
	})
	if a := msgs[len(msgs)-1].Content; strings.Contains(a, "DIRECTION") {
		t.Errorf("an empty direction still wrote a heading:\n%s", a)
	}
}
