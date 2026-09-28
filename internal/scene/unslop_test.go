package scene

import (
	"context"
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
)

// scripted is a model that writes each of its replies in small chunks, one
// reply per request, and keeps what it was asked.
type scripted struct {
	replies []string
	asked   [][]ollama.Message
	// copies is a model whose chat template closes a trailing reply: shown
	// one, it writes it out again before carrying on.
	copies bool
}

func (s *scripted) chat(ctx context.Context, msgs []ollama.Message, onDelta func(ollama.Delta)) (ollama.Message, ollama.Stats, error) {
	s.asked = append(s.asked, msgs)
	i := len(s.asked) - 1
	reply := s.replies[min(i, len(s.replies)-1)]
	if last := msgs[len(msgs)-1]; s.copies && last.Role == ollama.RoleAssistant {
		reply = last.Content + reply
	}
	var out strings.Builder
	for j := 0; j < len(reply); j += 3 {
		if err := ctx.Err(); err != nil {
			return ollama.Message{Content: out.String()}, ollama.Stats{Tokens: j / 3}, err
		}
		chunk := reply[j:min(j+3, len(reply))]
		out.WriteString(chunk)
		onDelta(ollama.Delta{Content: chunk})
	}
	return ollama.Message{Role: ollama.RoleAssistant, Content: out.String()}, ollama.Stats{Tokens: len(reply) / 3}, nil
}

func closing() []ollama.Message {
	return []ollama.Message{
		{Role: ollama.RoleSystem, Content: "You are Vesper."},
		{Role: ollama.RoleUser, Content: "Hello."},
		{Role: ollama.RoleSystem, Content: "[Closing block.]"},
	}
}

func TestUnslopCutsAStockPhraseBeforeItIsShown(t *testing.T) {
	m := &scripted{replies: []string{
		`*She looks up from the chart, and a shiver ran down her spine as the door opened.* "You're late."`,
		` went cold as the door opened.* "You're late."`,
	}}
	var shown strings.Builder
	msg, _, err := Unslop(context.Background(), m.chat, closing(), func(d ollama.Delta) { shown.WriteString(d.Content) })
	if err != nil {
		t.Fatal(err)
	}
	want := `*She looks up from the chart, and a went cold as the door opened.* "You're late."`
	if msg.Content != want || shown.String() != want {
		t.Fatalf("reply %q\nshown %q", msg.Content, shown.String())
	}
	if len(m.asked) != 2 {
		t.Fatalf("asked %d times", len(m.asked))
	}
	second := m.asked[1]
	if last := second[len(second)-1]; last.Role != ollama.RoleAssistant || last.Content != `*She looks up from the chart, and a` {
		t.Fatalf("carried on from %q", last.Content)
	}
	if block := second[len(second)-2].Content; !strings.Contains(block, `Do not write "shiver … down … spine"`) || !strings.HasSuffix(block, "]") {
		t.Fatalf("closing block %q", block)
	}
	if strings.Contains(shown.String(), "shiver") {
		t.Fatal("the stock phrase reached the screen")
	}
}

func TestUnslopKeepsAPrefillAndGivesUpOnARepeat(t *testing.T) {
	m := &scripted{replies: []string{
		`She smirks at you.`,
		` smirks again.`,
		` smirks a third time, and it stands.`,
	}}
	msgs := append(closing(), ollama.Message{Role: ollama.RoleAssistant, Content: "*"})
	msg, _, err := Unslop(context.Background(), m.chat, msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.asked) != 3 {
		t.Fatalf("asked %d times", len(m.asked))
	}
	if got := m.asked[1][len(m.asked[1])-1].Content; got != "*She" {
		t.Fatalf("the prefill was not kept in front: %q", got)
	}
	if msg.Content != "She smirks a third time, and it stands." {
		t.Fatalf("reply %q", msg.Content)
	}
}

func TestUnslopLeavesWordsThatOnlyStartLikeAPhrase(t *testing.T) {
	m := &scripted{replies: []string{`It is a testament tobacco would never make.`}}
	msg, _, _ := Unslop(context.Background(), m.chat, closing(), nil)
	if len(m.asked) != 1 || msg.Content != `It is a testament tobacco would never make.` {
		t.Fatalf("asked %d times, reply %q", len(m.asked), msg.Content)
	}
	if _, at, _ := chars.FindStock("a testament to her patience", nil); at < 0 {
		t.Fatal("testament to was not found")
	}
	if _, at, _ := chars.FindStock("She couldn’t help but laugh", nil); at < 0 {
		t.Fatal("a curly apostrophe hid the phrase")
	}
}

func TestUnslopStoppedKeepsWhatWasWritten(t *testing.T) {
	m := &scripted{replies: []string{strings.Repeat("The rain keeps on. ", 40)}}
	ctx, cancel := context.WithCancel(context.Background())
	var shown strings.Builder
	n := 0
	msg, _, err := Unslop(ctx, m.chat, closing(), func(d ollama.Delta) {
		shown.WriteString(d.Content)
		if n++; n == 20 {
			cancel()
		}
	})
	if err == nil {
		t.Fatal("a stop should report itself")
	}
	if msg.Content == "" || shown.String() != msg.Content {
		t.Fatalf("stopped: reply %d chars, shown %d", len(msg.Content), shown.Len())
	}
}

func TestUnslopWithAModelThatWritesTheReplyAgain(t *testing.T) {
	for _, copies := range []bool{false, true} {
		m := &scripted{copies: copies, replies: []string{
			`*She looks up from the chart, and a shiver ran down her spine as the door opened.* "You're late."`,
			` chill went through her as the door opened.* "You're late."`,
		}}
		var shown strings.Builder
		msg, _, err := Unslop(context.Background(), m.chat, closing(), func(d ollama.Delta) { shown.WriteString(d.Content) })
		if err != nil {
			t.Fatal(err)
		}
		want := `*She looks up from the chart, and a chill went through her as the door opened.* "You're late."`
		if msg.Content != want || shown.String() != want {
			t.Fatalf("copies %v: reply %q\nshown %q", copies, msg.Content, shown.String())
		}
	}
}

func TestContinueTakesTheCopyOff(t *testing.T) {
	msgs := append(closing(), ollama.Message{Role: ollama.RoleAssistant, Content: "The old ginger cat stretched out lazily on the"})
	for _, copies := range []bool{false, true} {
		m := &scripted{copies: copies, replies: []string{" woven mat."}}
		msg, _, err := Continue(context.Background(), m.chat, msgs, nil)
		if err != nil || msg.Content != " woven mat." {
			t.Fatalf("copies %v: %q %v", copies, msg.Content, err)
		}
	}
}
