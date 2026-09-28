package chars

// A controlled comparison of prompt and sampler arms, played against a real
// model through the same assembly the application uses.
//
//	ASTRAL_ARMS=repetition ASTRAL_TEST_MODEL=huihui_ai/qwen3.6-abliterated:27b \
//	    go test ./internal/chars/ -run TestLiveArms -v -timeout 90m
//
// Each arm plays the same scripted scene from the same opening, several times,
// and reports numbers that can be counted rather than judged. The character is
// deliberately not the one the prompt's own examples are written about, so an
// example phrase turning up in a reply is leakage from the prompt and not the
// scene talking about itself.

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
)

var armsCharacter = Character{
	Name: "Dagny Hale",
	Description: "Runs the night shift at a salvage yard on the edge of a flooded city. Forties, " +
		"broad-shouldered, grease under every nail. Owes money to the wrong people and has told no one.",
	Personality: "dry, practical, protective, hides worry behind sarcasm",
	Appearance:  "Oil-stained coveralls rolled to the elbow, a knit cap, a scar through one eyebrow.",
	Speech: "Crude and vulgar: swears in almost every line. Short sentences. Calls people \"kid\" when " +
		"she is worried about them. Never says what she feels directly.",
	Scenario: "{{user}} is Dagny's younger brother, back after two years away. It is two in the morning, " +
		"raining, and the yard's generator is failing.",
	FirstMes: `*The floodlights flicker when the generator coughs, and Dagny kicks it without looking.* "Gate's locked. Whoever you are, we're closed."`,
}

var armsPlayer = []string{
	`*I step through the gap in the fence, soaked, and raise a hand.* "Hey, Dag."`,
	`"I heard about the loan. Mom told me."`,
	`*I pick up a wrench and start on the generator without asking.*`,
	`"Who are you in debt to?"`,
	`*Headlights sweep across the yard. A car stops at the gate and two people get out.*`,
	`"Do you know them?"`,
	`*I stand next to her and don't say anything.*`,
	`"What do they want?"`,
	`*After they leave, I sit down on an overturned crate.*`,
	`"Why didn't you call me?"`,
	`*I hand her the thermos from my bag.*`,
	`"I can help. I've got some money saved."`,
	`*The generator coughs and dies. Everything goes dark.*`,
	`"Dag. Talk to me."`,
	`*I wait.*`,
	`"Okay. Then we do it together."`,
}

// examplePhrases are lifted from the prompt's own format examples. None of them
// belongs in a salvage yard.
var examplePhrases = []string{
	"did not look up", "deliberate violence", "dripping on", "found the window",
	"rather than the map", "pin went into", "you're late", "the sever",
	// And the shapes that replace them, in case a small model copies those.
	"in asterisks", "in quotes", "the next beat", "more speech", "narration...", "...speech",
}

var stockNames = regexp.MustCompile(`\b(Elara|Seraphina|Lyra|Kael|Aria|Borin|Thorne|Elias|Silas|Marcus|Viktor|Vex|Vance|Kael[a-z]*)\b`)
var thirdPersonBrother = regexp.MustCompile(`(?i)\bher (little |younger |kid )?brother\b`)
var strayScript = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]+`)
var insultAtYou = regexp.MustCompile(`(?i)\byou(?:'re| are)? (?:a |an |fucking |stupid |little )*(?:idiot|moron|asshole|bastard|bitch|dumbass|prick|shit|piece of shit|fuckhead|dickhead)\b`)
var quoted = regexp.MustCompile(`"[^"]*"|“[^”]*”`)
var youActs = regexp.MustCompile(`(?:^|[.!?*\n]\s*)You\b`)
var beforeYou = regexp.MustCompile(`(?i)before (you|he|she|they) can (respond|answer|react|say)`)

type arm struct {
	name     string
	system   func(string) string // rewrites the system message, or nil
	anchor   func(string) string // rewrites the closing block, or nil
	overused bool
	opts     func(ollama.Options) ollama.Options
}

type armResult struct {
	rep, words, unmarked, qEnd float64
	leaks, stock, before       int
	thirdPerson, named         int
	phrasesAtEnd               int
	// cjk counts stray Han, kana and hangul in an English scene: a token
	// sampled from the tail of a flat distribution, which the MoE produces
	// now and then.
	cjk int
	// swears counts every swear, and swearTop the most used one, so the
	// share of swearing that is one word can be read off.
	swears, swearTop int
	// insults counts swearing aimed at the listener, which is what a
	// character written as "vulgar" tends to turn into.
	insults int
	// userActs counts sentences outside quotation marks that begin "You":
	// the reply narrating the user's own actions, the rule that matters most.
	userActs int
}

func TestLiveArms(t *testing.T) {
	which := os.Getenv("ASTRAL_ARMS")
	if which == "" {
		t.Skip("set ASTRAL_ARMS to repetition or framing")
	}
	client, model := liveModel(t)
	runs := 2
	if n, err := strconv.Atoi(os.Getenv("ASTRAL_RUNS")); err == nil && n > 0 {
		runs = n
	}
	turns := len(armsPlayer)
	if n, err := strconv.Atoi(os.Getenv("ASTRAL_TURNS")); err == nil && n > 0 && n < turns {
		turns = n
	}

	base := func(o ollama.Options) ollama.Options { return o }
	var arms []arm
	switch which {
	case "repetition":
		arms = []arm{
			{name: "baseline", opts: base},
			{name: "anchor", overused: true, opts: base},
			// 0.3 collapsed the MoE into word salad inside four turns, so the
			// frequency arm is tried far lower, and min_p is tried as the
			// gentler alternative: it trims the tail where salad starts
			// rather than pushing probability into it.
			{name: "freq0.1", opts: func(o ollama.Options) ollama.Options { o.FrequencyPenalty = 0.1; return o }},
			{name: "anchor+minp0.05", overused: true, opts: func(o ollama.Options) ollama.Options { o.MinP = 0.05; return o }},
		}
	case "framing":
		arms = framingArms()
	case "behaviour":
		arms = behaviourArms()
	case "confirm":
		arms = confirmArms()
	case "sister":
		arms = sisterArms()
	default:
		t.Fatalf("no such experiment %q", which)
	}

	p := Persona{Name: "Wren", Style: DefaultStyle()}
	results := map[string][]armResult{}
	for r := 0; r < runs; r++ {
		// Interleaved rather than one arm after another, so drift in the
		// machine over the length of the run (thermals, another process)
		// lands on every arm equally.
		for _, a := range arms {
			res, replies := playArm(t, client, model, a, p, turns)
			results[a.name] = append(results[a.name], res)
			if r == 0 {
				t.Logf("--- %s, run 1, turns 8 and last ---\n%s\n...\n%s", a.name, replies[min(7, len(replies)-1)], replies[len(replies)-1])
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\nmodel %s, %d runs x %d turns\n", model, runs, turns)
	fmt.Fprintf(&b, "%-22s %6s %6s %8s %6s %6s %6s %6s %6s %6s %6s %5s %6s %6s %6s %6s\n",
		"arm", "rep", "words", "unmarked", "q_end", "leaks", "stock", "before", "3rdP", "named", "habits",
		"cjk", "swears", "top", "insult", "youAct")
	names := make([]string, 0, len(arms))
	for _, a := range arms {
		names = append(names, a.name)
	}
	for _, name := range names {
		var m armResult
		rs := results[name]
		for _, r := range rs {
			m.rep += r.rep / float64(len(rs))
			m.words += r.words / float64(len(rs))
			m.unmarked += r.unmarked / float64(len(rs))
			m.qEnd += r.qEnd / float64(len(rs))
			m.leaks += r.leaks
			m.stock += r.stock
			m.before += r.before
			m.thirdPerson += r.thirdPerson
			m.named += r.named
			m.phrasesAtEnd += r.phrasesAtEnd
			m.cjk += r.cjk
			m.swears += r.swears
			m.swearTop += r.swearTop
			m.insults += r.insults
			m.userActs += r.userActs
		}
		reps := make([]string, len(rs))
		for i, r := range rs {
			reps[i] = fmt.Sprintf("%.3f/n%d/y%d", r.rep, r.named, r.userActs)
		}
		fmt.Fprintf(&b, "%-22s %6.3f %6.0f %8.1f %6.2f %6d %6d %6d %6d %6d %6d %5d %6d %6d %6d %6d   rep per run %s\n",
			name, m.rep, m.words, m.unmarked, m.qEnd, m.leaks, m.stock, m.before, m.thirdPerson, m.named, m.phrasesAtEnd,
			m.cjk, m.swears, m.swearTop, m.insults, m.userActs, strings.Join(reps, " "))
	}
	t.Log(b.String())
}

func playArm(t *testing.T, client *ollama.Client, model string, a arm, p Persona, turns int) (armResult, []string) {
	t.Helper()
	c := armsCharacter
	hist := []ollama.Message{{Role: ollama.RoleAssistant, Content: Greeting(c, p)}}
	var replies []string
	no := false
	for i := 0; i < turns; i++ {
		hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: armsPlayer[i]})
		sc := Scene{
			Persona:          p,
			History:          hist,
			Budget:           Plan(8192, 400, len(BuildSystem(c, p))),
			NarrationDrifted: NarrationDrifted(hist),
		}
		if a.overused {
			sc.Overused = Overused(hist)
		}
		msgs := BuildMessages(c, sc)
		if a.system != nil {
			msgs[0].Content = a.system(msgs[0].Content)
		}
		if a.anchor != nil {
			last := len(msgs) - 1
			msgs[last].Content = a.anchor(msgs[last].Content)
		}
		opts := a.opts(ollama.Options{
			Temperature: 0.85, TopP: 0.92, RepeatPenalty: 1.08, RepeatLastN: 384,
			NumCtx: 8192, NumPredict: 400,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		reply, _, err := client.Chat(ctx, model, msgs, opts, &no, nil)
		cancel()
		if err != nil {
			// A reply the server aborted for looping is a result, not a
			// failure of the harness: it is the worst case of the thing
			// being measured, so it is recorded as a full repeat.
			if strings.Contains(err.Error(), "repeat limit") {
				replies = append(replies, "[aborted for repeating]")
				hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: "*She says nothing.*"})
				continue
			}
			t.Fatalf("%s turn %d: %v", a.name, i+1, err)
		}
		_, body := ollama.SplitThinking(reply.Content)
		body = strings.TrimSpace(body)
		replies = append(replies, body)
		hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: body})
	}
	return measureReplies(replies, p.Name, hist), replies
}

func measureReplies(replies []string, userName string, hist []ollama.Message) armResult {
	var r armResult
	scored := 0
	counts := map[string]int{}
	for i, reply := range replies {
		if reply == "[aborted for repeating]" {
			r.rep += 1
			scored++
			continue
		}
		if i >= 2 {
			start := max(0, i-5)
			r.rep += RepetitionScore(reply, replies[start:i])
			scored++
		}
		r.words += float64(len(strings.Fields(reply)))
		r.unmarked += float64(unmarkedProse(reply))
		trimmed := strings.TrimRight(reply, "*\"” \n")
		if strings.HasSuffix(trimmed, "?") {
			r.qEnd++
		}
		low := strings.ToLower(reply)
		for _, ph := range examplePhrases {
			r.leaks += strings.Count(low, ph)
		}
		r.stock += len(stockNames.FindAllString(reply, -1))
		r.before += len(beforeYou.FindAllString(reply, -1))
		r.thirdPerson += len(thirdPersonBrother.FindAllString(reply, -1))
		r.named += strings.Count(reply, userName)
		r.cjk += len(strayScript.FindAllString(reply, -1))
		r.insults += len(insultAtYou.FindAllString(reply, -1))
		r.userActs += len(youActs.FindAllString(quoted.ReplaceAllString(reply, " "), -1))
		for _, w := range wordsOf(reply) {
			if root, ok := swearRoots[w]; ok {
				r.swears++
				counts[root]++
			}
		}
	}
	for _, n := range counts {
		if n > r.swearTop {
			r.swearTop = n
		}
	}
	n := float64(len(replies))
	if scored > 0 {
		r.rep /= float64(scored)
	}
	r.words /= n
	r.unmarked /= n
	r.qEnd /= n
	r.phrasesAtEnd = len(Overused(hist).Phrases)
	return r
}

// The example blocks the arms replace, as they appear in the prompt.
const (
	oldSystemExample = `Example of a full reply:
*She did not look up from the chart when you came in. The rain had found the window again, and she let it.* "You're late."
*A pin went into the table rather than the map, a small and deliberate violence.* "Sit. You're dripping on the Sever."`
	oldAnchorExample = `Example:
*She did not look up from the chart.* "You're late."

*A pin went into the table rather than the map.* "Sit."`
	oldFirmExample = `Example:
*She did not look up from the chart. The rain had found the window again, and she let it.* "You're late."
*A pin went into the table rather than the map, a small and deliberate violence.* "Sit. You're dripping on the Sever."`
)

// Two content-free shapes: one that describes each part in words, one that is
// punctuation only.
const (
	shapeWords = `The shape of a reply (the shape only; the content is yours):
*What {{char}} does, notices or thinks.* "What {{char}} says out loud."

*The next beat.* "More of what {{char}} says, if anything."`
	shapeDots = `The shape of a reply, with ... where your own words go:
*...* "..."

*...* "..."`
)

const oldOpening = "You are roleplaying as {{char}}. Stay in character at all times."

const authorOpening = "You are a skilled author writing an interactive story together with {{user}}. " +
	"You write {{char}} and everything around them, what they say and do, the place, and anyone else " +
	"in the scene, and you give {{char}} their full voice without holding back. {{user}} writes their own part."

const behaviour = `
Keep the scene moving: give {{char}} something to want, notice or do, not only something to react to.
End each reply where {{user}} can act, on something said or done that invites a response. Do not end on a question unless {{char}} would really ask it, and never write that {{user}} is about to answer or cannot.
Match {{user}}'s pace: a short line gets a short reply, a long one can get more.
If {{char}} swears or is described as crude or vulgar, that is how they talk: swearing lands on frustration, surprise and emphasis, not on the person they are talking to unless they mean to insult them. Vary the words rather than leaning on one.
Give new people names that fit the setting, not stock names like Elara, Seraphina, Kael, Lyra or Vance.`

// framingArms compare the example the prompt shows, and then the framing
// around it.
func framingArms() []arm {
	base := func(o ollama.Options) ollama.Options { return o }
	sub := func(s string) string { return Substitute(s, armsCharacter.Name, "Wren") }
	swapExamples := func(shape string) (func(string) string, func(string) string) {
		sys := func(s string) string { return strings.Replace(s, oldSystemExample, sub(shape), 1) }
		anc := func(s string) string {
			s = strings.Replace(s, oldAnchorExample, sub(shape), 1)
			return strings.Replace(s, oldFirmExample, sub(shape), 1)
		}
		return sys, anc
	}
	wordsSys, wordsAnc := swapExamples(shapeWords)
	dotsSys, dotsAnc := swapExamples(shapeDots)
	author := func(shapeSys func(string) string) func(string) string {
		return func(s string) string {
			s = shapeSys(s)
			s = strings.Replace(s, sub(oldOpening), sub(authorOpening), 1)
			// The behaviour lines join the list of what to write.
			return strings.Replace(s, "do not end the scene on your own.", "do not end the scene on your own."+sub(behaviour), 1)
		}
	}
	return []arm{
		{name: "current", opts: base},
		{name: "current+fresh", overused: true, opts: base},
		{name: "shapeWords+fresh", overused: true, system: wordsSys, anchor: wordsAnc, opts: base},
		{name: "shapeDots+fresh", overused: true, system: dotsSys, anchor: dotsAnc, opts: base},
		{name: "author+words+fresh", overused: true, system: author(wordsSys), anchor: wordsAnc, opts: base},
		{name: "author+dots+fresh", overused: true, system: author(dotsSys), anchor: dotsAnc, opts: base},
	}
}

// behaviourArms isolate the behaviour lines under the character framing, with
// the example the framing experiment chose.
func behaviourArms() []arm {
	base := func(o ollama.Options) ollama.Options { return o }
	sub := func(s string) string { return Substitute(s, armsCharacter.Name, "Wren") }
	sys := func(s string) string { return strings.Replace(s, oldSystemExample, sub(shapeDots), 1) }
	anc := func(s string) string {
		s = strings.Replace(s, oldAnchorExample, sub(shapeDots), 1)
		return strings.Replace(s, oldFirmExample, sub(shapeDots), 1)
	}
	withBehaviour := func(s string) string {
		return strings.Replace(sys(s), "do not end the scene on your own.", "do not end the scene on your own."+sub(behaviour), 1)
	}
	return []arm{
		{name: "dots+fresh", overused: true, system: sys, anchor: anc, opts: base},
		{name: "dots+fresh+behaviour", overused: true, system: withBehaviour, anchor: anc, opts: base},
	}
}

// confirmArms run against the production prompt as it now stands, which has
// the reply shape in place of the example and the reworded "who is who" line.
func confirmArms() []arm {
	base := func(o ollama.Options) ollama.Options { return o }
	sub := func(s string) string { return Substitute(s, armsCharacter.Name, "Wren") }
	newWho := "Write to {{user}}, not about them: call them you, never by name and never he or she. Write *as you came in*, never *as {{user}} came in*, and your sister rather than her sister."
	oldWho := "Write to {{user}}, not about them: call them you, never by name and never he or she. *She did not look up as you came in*, and your sister rather than her sister."
	withBehaviour := func(s string) string {
		return strings.Replace(s, "do not end the scene on your own.", "do not end the scene on your own."+sub(behaviour), 1)
	}
	return []arm{
		{name: "production", overused: true, opts: base},
		{name: "production+behaviour", overused: true, system: withBehaviour, opts: base},
		{name: "production+oldWho", overused: true, system: func(s string) string {
			return strings.Replace(s, sub(newWho), sub(oldWho), 1)
		}, opts: base},
	}
}

// sisterArms test the example of writing to the person playing. It named a
// relative, "your sister rather than her sister", and a relative is also
// something a character can call somebody: measured on a dockside bar scene,
// ten replies in forty had the barkeep calling the person playing "sister",
// against none with the example about a coat, which production now uses. This
// scene is the one the old example was written for, where the person playing
// is somebody's brother, so it checks the coat still keeps them "you": on
// SOMPOA over three runs, no third-person references and no names either way.
func sisterArms() []arm {
	base := func(o ollama.Options) ollama.Options { return o }
	sister := func(s string) string {
		s = strings.ReplaceAll(s, "your coat rather than her coat", "your sister rather than her sister")
		return strings.ReplaceAll(s, "your coat, not her coat", "your sister, not her sister")
	}
	return []arm{
		{name: "production", overused: true, opts: base},
		{name: "sister", overused: true, system: sister, anchor: sister, opts: base},
	}
}
