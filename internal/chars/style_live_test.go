package chars

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

// The complaint this measures: "even after changing the writing style it still
// kind of sounds similar". That is not a thing that can be settled by reading
// the prompt, so it is settled by running two very different styles through a
// real model on the same scene and measuring what comes back.
//
// Skips itself without a server, like the rest of the live tests.
func liveClient(t *testing.T) (*ollama.Client, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("live test skipped in -short mode")
	}
	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	models, err := client.Probe(ctx)
	if err != nil {
		t.Skipf("no Ollama server reachable: %v", err)
	}
	if len(models) == 0 {
		t.Skip("Ollama is running but has no models installed")
	}
	model := os.Getenv("ASTRAL_TEST_MODEL")
	if model == "" {
		model = models[0].Name
	}
	return client, model
}

// prose describes a reply in the terms a writing style actually talks about.
type prose struct {
	words         int
	paragraphs    int
	meanSentence  float64
	dialogueRatio float64 // share of characters inside "quotes"
	markedRatio   float64 // share of narration inside *asterisks*
}

func measure(s string) prose {
	p := prose{}
	p.words = len(strings.Fields(s))
	for _, para := range strings.Split(s, "\n") {
		if strings.TrimSpace(para) != "" {
			p.paragraphs++
		}
	}
	sentences := 0
	for _, r := range s {
		if r == '.' || r == '!' || r == '?' {
			sentences++
		}
	}
	if sentences > 0 {
		p.meanSentence = float64(p.words) / float64(sentences)
	}

	var inQuotes, inStars bool
	var quoted, starred, bare int
	for _, r := range s {
		switch r {
		case '"', '“', '”':
			inQuotes = !inQuotes
			continue
		case '*':
			inStars = !inStars
			continue
		}
		if r == ' ' || r == '\n' || r == '\t' {
			continue
		}
		switch {
		case inQuotes:
			quoted++
		case inStars:
			starred++
		default:
			bare++
		}
	}
	if total := quoted + starred + bare; total > 0 {
		p.dialogueRatio = float64(quoted) / float64(total)
	}
	if narration := starred + bare; narration > 0 {
		p.markedRatio = float64(starred) / float64(narration)
	}
	return p
}

func (p prose) String() string {
	return fmt.Sprintf("%d words, %d paragraphs, %.1f words/sentence, %.0f%% dialogue, %.0f%% of narration marked",
		p.words, p.paragraphs, p.meanSentence, 100*p.dialogueRatio, 100*p.markedRatio)
}

func liveReply(t *testing.T, client *ollama.Client, model string, c Character, sc Scene) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	noThink := false
	msg, _, err := client.Chat(ctx, model, BuildMessages(c, sc),
		ollama.Options{NumCtx: 8192, Temperature: 0.85, TopP: 0.92, NumPredict: DefaultReplyTokens},
		&noThink, nil)
	if err != nil {
		if ctx.Err() != nil {
			t.Skipf("model too slow for this test: %v", err)
		}
		t.Fatalf("chat: %v", err)
	}
	return strings.TrimSpace(msg.Content)
}

var (
	terseStyle = WritingStyle{Name: "Clipped", Instructions: `Length: One short paragraph, never more.
Sentences: Short and flat. Rarely more than eight words. No subordinate clauses.
Tense and person: Third person, past tense.
Description: Almost none. One physical detail at most, and only if it carries weight.
Dialogue: Blunt and sparing. {{char}} answers in a few words and does not elaborate.
Avoid: Any sentence longer than a line. Lists of sensory detail. Explaining what anyone feels.`}

	lushStyle = WritingStyle{Name: "Lavish", Instructions: `Length: Four full paragraphs, and use all of them.
Sentences: Long and winding, with clauses folded into clauses, and a short one only to close a paragraph.
Tense and person: Third person, past tense.
Description: Heavy and continuous. Dwell on light, texture, temperature and smell in every paragraph.
Dialogue: Sparse. {{char}} says little; the weight of the reply is in what is described around the words.
Avoid: Getting to the point quickly. Leaving a room undescribed. Short paragraphs.`}
)

func styleScene(style WritingStyle, hist []ollama.Message) Scene {
	return Scene{
		Persona: Persona{Name: "Christian", Style: style},
		History: hist,
	}
}

func TestLiveStyleActuallyChangesTheProse(t *testing.T) {
	client, model := liveClient(t)
	c := Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet. Tall, ink to the elbows, never without her brass dividers.",
		Scenario:    "Her map room, late, during a storm.",
	}
	hist := []ollama.Message{
		{Role: ollama.RoleUser, Content: "I push the door open and shake the rain off my coat."},
	}

	terse := liveReply(t, client, model, c, styleScene(terseStyle, hist))
	lush := liveReply(t, client, model, c, styleScene(lushStyle, hist))

	mt, ml := measure(terse), measure(lush)
	t.Logf("model: %s", model)
	t.Logf("clipped: %s", mt)
	t.Logf("lavish:  %s", ml)

	// The styles ask for one short paragraph against four full ones. If the
	// style is reaching the model at all, the lavish reply must be clearly
	// longer. "Clearly" is 1.8x: below that the two are the same reply with
	// different adjectives, which is exactly the complaint.
	if ratio := float64(ml.words) / math.Max(1, float64(mt.words)); ratio < 1.8 {
		t.Errorf("lavish reply is only %.2fx the length of the clipped one (%d vs %d words): "+
			"the style is not reaching the model", ratio, ml.words, mt.words)
	}
}

// The second complaint: narration sometimes arrives with no asterisks at all.
// Measured over several replies, because one is not evidence.
func TestLiveNarrationIsMarked(t *testing.T) {
	client, model := liveClient(t)
	c := Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet.",
		Scenario:    "Her map room, late, during a storm.",
	}
	prompts := []string{
		"I push the door open and shake the rain off my coat.",
		"I set the ruined chart on her desk without a word.",
		"\"You said three days. It's been nine.\"",
	}
	var worst float64 = 1
	for _, p := range prompts {
		reply := liveReply(t, client, model, c, styleScene(DefaultStyle(),
			[]ollama.Message{{Role: ollama.RoleUser, Content: p}}))
		m := measure(reply)
		t.Logf("%-52q -> %s", p, m)
		if m.markedRatio < worst {
			worst = m.markedRatio
		}
	}
	// Some unmarked text is tolerable: a stray connective between two quoted
	// lines is normal. A reply where most narration is unmarked is the bug.
	if worst < 0.75 {
		t.Errorf("one reply had only %.0f%% of its narration in asterisks", 100*worst)
	}
}

// The escalation, end to end. A transcript where the character's replies have
// been arriving unmarked is exactly the state the gentle rule has already
// failed in, so the question is whether the firmer wording recovers it.
func TestLiveDriftEscalationRecovers(t *testing.T) {
	client, model := liveClient(t)
	c := Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet.",
		Scenario:    "Her map room, late, during a storm.",
	}
	// A scene whose replies set the wrong precedent: narration, unmarked.
	drifted := []ollama.Message{
		{Role: ollama.RoleUser, Content: "I push the door open."},
		{Role: ollama.RoleAssistant, Content: `She did not look up from the chart. The rain had found the window again and she let it. "You're late."`},
		{Role: ollama.RoleUser, Content: "I shake the water off my coat."},
		{Role: ollama.RoleAssistant, Content: `A pin went into the table rather than the map. She watched him drip onto her floor and said nothing about it for a while. "Sit."`},
		{Role: ollama.RoleUser, Content: "I sit, and wait for her to say something else."},
	}
	if !NarrationDrifted(drifted) {
		t.Fatal("the fixture is not detected as drifted, so this test proves nothing")
	}

	sc := styleScene(DefaultStyle(), drifted)
	sc.NarrationDrifted = true

	// The same two measures the app applies together, because separately they
	// were not enough: on a 24B roleplay finetune the firmer wording alone
	// recovered none of the markup, and the prefill recovered all of it.
	msgs := append(BuildMessages(c, sc), ollama.Message{Role: ollama.RoleAssistant, Content: NarrationPrefill})

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	noThink := false
	reply, _, err := client.Chat(ctx, model, msgs,
		ollama.Options{NumCtx: 8192, Temperature: 0.85, TopP: 0.92, NumPredict: DefaultReplyTokens},
		&noThink, nil)
	if err != nil {
		if ctx.Err() != nil {
			t.Skipf("model too slow for this test: %v", err)
		}
		t.Fatalf("chat: %v", err)
	}

	m := measure(RestorePrefill(strings.TrimSpace(reply.Content)))
	t.Logf("model: %s", model)
	t.Logf("reply after escalation and prefill: %s", m)

	if m.markedRatio < 0.75 {
		t.Errorf("after escalating the format rule and prefilling, only %.0f%% of narration "+
			"was marked: the transcript's own precedent is still winning", 100*m.markedRatio)
	}
}

// A direction has two ways to fail and they pull in opposite directions. It
// can be ignored, which makes the control useless; or it can be obeyed too
// literally, with the model narrating the instruction itself — "she was about
// to realise he had lied" — which is worse than ignoring it, because it hands
// the reader the thing the scene was supposed to play out.
//
// So arrival is measured over several turns rather than one. The prompt tells
// the model to move one step and not to get there in a single reply, and an
// earlier version of this test then asserted that it had got there in a single
// reply. It had not, and it was right not to: it stood the character up, put
// the charts in her arms and had her say "the tide waits for no one". The test
// was wrong, not the feature.
func TestLiveDirectionSteersWithoutAnnouncing(t *testing.T) {
	client, model := liveClient(t)
	c := Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet.",
		Scenario:    "Her map room, late, during a storm.",
	}
	hist := []ollama.Message{
		{Role: ollama.RoleUser, Content: "I push the door open and shake the rain off my coat."},
		{Role: ollama.RoleAssistant, Content: `*She did not look up from the chart.* "You're late."`},
		{Role: ollama.RoleUser, Content: "\"The ferry was late, not me.\""},
	}

	// A target that cannot happen by accident. An earlier version aimed the
	// scene at the harbour, and the control group promptly went to the harbour
	// on its own: a storm, a ferry and a cartographer drift seawards without
	// anyone asking. A sister who has never been mentioned does not.
	const direction = "Get {{char}} talking about her sister."
	arrived := []string{"sister", "sibling"}

	// Passive user turns. If the scene reaches the harbour anyway, it is the
	// direction doing the work and not the person playing.
	passive := []string{"I watch her.", "I say nothing.", "I wait."}

	play := func(dir string) (string, int) {
		turns := append([]ollama.Message{}, hist...)
		for i, u := range passive {
			if i > 0 {
				turns = append(turns, ollama.Message{Role: ollama.RoleUser, Content: u})
			}
			sc := styleScene(DefaultStyle(), turns)
			sc.Direction = dir
			reply := liveReply(t, client, model, c, sc)
			turns = append(turns, ollama.Message{Role: ollama.RoleAssistant, Content: reply})
			low := strings.ToLower(reply)
			for _, w := range arrived {
				if strings.Contains(low, w) {
					return reply, i + 1
				}
			}
		}
		return "", 0
	}

	withReply, withTurn := play(direction)
	t.Logf("model: %s", model)
	if withTurn == 0 {
		t.Errorf("after %d turns the direction was never taken up, so it did nothing", len(passive))
	} else {
		t.Logf("direction taken up on turn %d of %d", withTurn, len(passive))
		t.Logf("--- that reply ---\n%s", withReply)
	}

	// The control group. Without the direction the scene should stay put, or
	// the test is measuring the scenario rather than the direction.
	if _, plainTurn := play(""); plainTurn != 0 {
		t.Logf("NOTE: the same thing happened on turn %d with no direction at all, "+
			"so this fixture does not isolate the direction well", plainTurn)
	}

	// Announcing it: quoting the instruction back as prose instead of playing
	// it. A long verbatim run from the direction is the giveaway.
	low := strings.ToLower(withReply)
	for _, phrase := range []string{"get vesper quill talking about", "get {{char}} talking about"} {
		if strings.Contains(low, phrase) {
			t.Errorf("the reply quotes the direction back rather than playing it: %q", phrase)
		}
	}
}
