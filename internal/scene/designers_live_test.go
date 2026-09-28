package scene

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/livetest"
	"astral/internal/ollama"
	"astral/internal/promptopt"
	"astral/internal/prompts"
	"astral/internal/store"
	"astral/internal/world"
)

// Heavy live tests of the three designers and the Prompt Optimizer.
//
// Each designer is taken through scripted conversations and then asked to build
// what was discussed; the optimizer is handed several of Astral's own prompts
// and a bad one of the kind people bring. The requests are assembled exactly as
// the window assembles them (Build, the tool loop, the default settings), with
// search off so nothing leaves the machine.
//
// The checks here are the countable half. The other half is reading what came
// back, which is why every transcript is written to ASTRAL_TEST_OUT: a designer
// can pass every count and still write a dull character.
//
// One request at a time, on the model ASTRAL_TEST_MODEL names, which livetest
// refuses unless it is small and fits beside what is loaded.

type designScript struct {
	name, kind string
	turns      []string
}

var designScripts = []designScript{
	{"persona: a smuggler", store.KindPersonaDesigner, []string{
		"I want to play a smuggler who runs cargo along a rainy coast.",
		"Woman, mid thirties, half-elf. Scarred hands, always in an oilskin coat.",
		"The harbourmaster owes her money and everyone knows it. She is dry, patient, hard to rattle.",
		"That's her. Call her Maren Voss. Build it.",
	}},
	{"vague detective", store.KindDesigner, []string{
		"A tired detective.",
		"Bitter about it, not funny. She lost her partner last year and blames herself.",
		"She drinks more than she admits and lies to her sergeant about where she goes at night.",
		"A rain-soaked city, roughly the 1950s. I think that's enough, build her.",
	}},
	{"ruthless cult leader", store.KindDesigner, []string{
		"I want a cult leader who genuinely believes he is saving people and is completely ruthless about it. No redemption arc.",
		"He never raises his voice. Soft-spoken, patient, terrifying.",
		"He wants {{user}} to join, and he'll use their younger sister, already a member, to get them.",
		"Good. That's enough, let's make him.",
	}},
	{"invent one", store.KindDesigner, []string{
		"Just invent someone for a cosy slice of life story. Surprise me.",
		"Sounds good, go with that.",
		"Perfect, we're done.",
	}},
	{"hardboiled style", store.KindStyleDesigner, []string{
		"Terse and hardboiled. Short sentences, present tense.",
		"Violence is quick and ugly, never glamorous. No purple prose.",
		"One to three paragraphs a reply. That's everything.",
	}},
	{"gothic style", store.KindStyleDesigner, []string{
		"Lush gothic horror. Slow dread, long sentences, lots of sensory detail about decay.",
		"Dialogue should be sparse and formal. Nobody says what they mean.",
		"That's it, build it.",
	}},
	{"bell-ringer city", store.KindWorldDesigner, []string{
		"A drowned coastal city where the tides are controlled by a guild of bell-ringers.",
		"The guild is corrupt and sells high tides to flood rivals' districts. Ordinary people live on the rooftops.",
		"Technology is roughly 1800s, with a little strange magic in the bells. That's enough, build it.",
	}},
	{"memory currency", store.KindWorldDesigner, []string{
		"A cyberpunk megacity where memories are traded as currency.",
		"The poor sell their childhoods to pay rent. The rich collect other people's first loves.",
		"Build it.",
	}},
}

func designModel(t *testing.T) (*ollama.Client, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("live test skipped in -short mode")
	}
	client, installed := livetest.Client(t)
	return client, livetest.Model(t, client, installed)
}

// report is where transcripts go, for reading.
type report struct {
	f *os.File
}

func newReport(t *testing.T, name string) *report {
	dir := os.Getenv("ASTRAL_TEST_OUT")
	if dir == "" {
		dir = t.TempDir()
	}
	os.MkdirAll(dir, 0o755)
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return &report{f: f}
}

func (r *report) printf(format string, a ...any) { fmt.Fprintf(r.f, format, a...) }

func openingFor(kind string) string {
	switch kind {
	case store.KindStyleDesigner:
		return chars.StyleDesignerOpening
	case store.KindWorldDesigner:
		return world.DesignerOpening
	case store.KindPersonaDesigner:
		return chars.PersonaDesignerOpening
	}
	return chars.DesignerOpening
}

// turn sends the conversation so far and returns the reply, exactly as the
// window would: Build, then the tool loop.
func turn(ctx context.Context, t *testing.T, client *ollama.Client, model string, st *store.Store,
	cfg store.Config, ch store.Chat, hist []ollama.Message) (string, []string) {
	t.Helper()
	msgs := Build(st, cfg, ch, chars.Character{}, hist)
	think := false
	r := Runner(client, cfg, st, ch.Kind, model, OptionsFor(cfg, ch.Kind), &think)
	msg, _, rounds, err := r.Run(ctx, msgs, nil)
	if err != nil {
		t.Fatalf("the model failed: %v", err)
	}
	var notes []string
	for _, rd := range rounds {
		if rd.Note != "" {
			notes = append(notes, rd.Note)
		}
	}
	_, out := ollama.SplitThinking(msg.Content)
	return strings.TrimSpace(out), notes
}

var hedgeWords = []string{
	"i should note", "are you sure", "have you considered making", "might want to soften",
	"be mindful", "tasteful", "i'd encourage you", "a more sympathetic", "it's important to",
	"please note", "content warning", "i can't", "i cannot", "as an ai",
}

func words(s string) int { return len(strings.Fields(s)) }

func dashes(s string) int { return strings.Count(s, "\u2014") + strings.Count(s, "\u2013") }

func TestLiveDesignersHeavy(t *testing.T) {
	client, model := designModel(t)
	st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	rep := newReport(t, "designers.txt")
	only := os.Getenv("ASTRAL_TEST_ONLY")
	controlPrompts(t)

	type tally struct{ replies, long, manyQ, json, hedges, button, dashes, repeats int }
	byKind := map[string]*tally{}

	runs := 1
	if n, err := fmt.Sscan(os.Getenv("ASTRAL_TEST_RUNS"), &runs); n != 1 || err != nil || runs < 1 {
		runs = 1
	}
	var scripts []designScript
	for r := 0; r < runs; r++ {
		scripts = append(scripts, designScripts...)
	}
	for _, sc := range scripts {
		if only != "" && !strings.Contains(sc.name, only) && !strings.Contains(sc.kind, only) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		ch := store.Chat{Kind: sc.kind}
		hist := []ollama.Message{{Role: ollama.RoleAssistant, Content: openingFor(sc.kind)}}
		tl := byKind[sc.kind]
		if tl == nil {
			tl = &tally{}
			byKind[sc.kind] = tl
		}
		rep.printf("\n\n######## %s (%s)\n", sc.name, sc.kind)
		for i, u := range sc.turns {
			hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: u})
			reply, notes := turn(ctx, t, client, model, st, cfg, ch, hist)
			hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: reply})
			low := strings.ToLower(reply)
			q := strings.Count(reply, "?")
			flags := []string{}
			tl.replies++
			if w := words(reply); w > 170 {
				tl.long++
				flags = append(flags, fmt.Sprintf("LONG %dw", w))
			}
			if q > 2 {
				tl.manyQ++
				flags = append(flags, fmt.Sprintf("%d QUESTIONS", q))
			}
			if strings.Contains(reply, "```json") || strings.Contains(reply, `"name":`) {
				tl.json++
				flags = append(flags, "WROTE JSON")
			}
			for _, h := range hedgeWords {
				if strings.Contains(low, h) {
					tl.hedges++
					flags = append(flags, "HEDGE "+h)
				}
			}
			if i > 0 {
				prev := hist[len(hist)-3].Content
				if len(prev) > 80 && len(reply) > 80 && prev[:80] == reply[:80] {
					tl.repeats++
					flags = append(flags, "REPEATS ITSELF")
				}
			}
			if n := dashes(reply); n > 0 {
				tl.dashes += n
				flags = append(flags, fmt.Sprintf("%d DASHES", n))
			}
			if i == len(sc.turns)-1 {
				if strings.Contains(low, "create character") || strings.Contains(low, "create style") ||
					strings.Contains(low, "create world") || strings.Contains(low, "create persona") {
					tl.button++
				} else {
					flags = append(flags, "NO BUTTON NAMED")
				}
			}
			rep.printf("\n--- user: %s\n--- reply (%dw, %d?) %v %v\n%s\n", u, words(reply), q, flags, notes, reply)
		}

		// Build what was discussed.
		opts := Options(cfg)
		switch sc.kind {
		case store.KindDesigner:
			c, err := chars.BuildFromConversation(ctx, client, model, hist, opts)
			if err != nil {
				t.Errorf("%s: building the character failed: %v", sc.name, err)
				break
			}
			rep.printf("\n=== BUILT CHARACTER\nname: %s\ndescription: %s\npersonality: %s\nappearance: %s\nspeech: %s\nscenario: %s\nfirst_mes: %s\nmes_example: %s\ntags: %v\n",
				c.Name, c.Description, c.Personality, c.Appearance, c.Speech, c.Scenario, c.FirstMes, c.MesExample, c.Tags)
			var miss []string
			for f, v := range map[string]string{"name": c.Name, "description": c.Description, "personality": c.Personality,
				"appearance": c.Appearance, "speech": c.Speech, "scenario": c.Scenario, "first_mes": c.FirstMes} {
				if strings.TrimSpace(v) == "" {
					miss = append(miss, f)
				}
			}
			if !strings.Contains(c.FirstMes, "*") || !strings.Contains(c.FirstMes, `"`) {
				miss = append(miss, "first_mes formatting")
			}
			if !strings.Contains(c.MesExample, "{{user}}") || !strings.Contains(c.MesExample, "{{char}}") {
				miss = append(miss, "mes_example speakers")
			}
			rep.printf("build problems: %v\n", miss)
		case store.KindStyleDesigner:
			s, err := chars.BuildStyleFromConversation(ctx, client, model, hist, opts)
			if err != nil {
				t.Errorf("%s: building the style failed: %v", sc.name, err)
				break
			}
			rep.printf("\n=== BUILT STYLE\nname: %s\n%s\n", s.Name, s.Instructions)
			var miss []string
			if !regexp.MustCompile(`(?i)length`).MatchString(s.Instructions) {
				miss = append(miss, "no length")
			}
			if strings.Contains(s.Instructions, "*") {
				miss = append(miss, "talks about asterisks")
			}
			rep.printf("build problems: %v\n", miss)
		case store.KindPersonaDesigner:
			p, err := chars.BuildPersonaFromConversation(ctx, client, model, hist, opts)
			if err != nil {
				t.Errorf("%s: building the persona failed: %v", sc.name, err)
				break
			}
			rep.printf("\n=== BUILT PERSONA\n%s\n%s\n", p.Name, p.Description())
			var miss []string
			for f, v := range map[string]string{"name": p.Name, "age": p.Age, "gender": p.Gender,
				"race": p.Race, "appearance": p.Appearance} {
				if strings.TrimSpace(v) == "" {
					miss = append(miss, f)
				}
			}
			if dashes(p.Description()) > 0 {
				miss = append(miss, "dashes")
			}
			rep.printf("build problems: %v\n", miss)
		case store.KindWorldDesigner:
			d, err := world.BuildFromConversation(ctx, client, model, hist, opts)
			if err != nil {
				t.Errorf("%s: building the world failed: %v", sc.name, err)
				break
			}
			rep.printf("\n=== BUILT WORLD\nname: %s\ndescription: %s\nrules: %s\n", d.World.Name, d.World.Description, d.World.Rules)
			abstract := 0
			for _, e := range d.Entries {
				rep.printf("  entry %q keys %v: %s\n", e.Name, e.Keys, e.Content)
				for _, k := range e.Keys {
					switch strings.ToLower(k) {
					case "history", "politics", "magic", "religion", "economy", "culture", "technology", "society", "power", "corruption":
						abstract++
					}
				}
			}
			rep.printf("build problems: entries=%d abstract keys=%d\n", len(d.Entries), abstract)
		}
		cancel()
	}
	for kind, tl := range byKind {
		msg := fmt.Sprintf("%s: %d replies, %d long, %d with >2 questions, %d repeated themselves, %d wrote JSON, %d hedges, %d dashes, button named %d times",
			kind, tl.replies, tl.long, tl.manyQ, tl.repeats, tl.json, tl.hedges, tl.dashes, tl.button)
		rep.printf("\n%s", msg)
		t.Log(msg)
	}
}

// optimizerCase is a prompt handed to the optimizer, with what its rewrite
// must still say, as patterns.
type optimizerCase struct {
	id      string   // a registered prompt, or "" for a brought one
	brought string   // the prompt, when brought
	keep    []string // regular expressions the rewrite must still match
	never   []string // regular expressions it must not match
}

var optimizerCases = []optimizerCase{
	{id: "scene.framing",
		keep:  []string{`\{\{char\}\}`, `\{\{user\}\}`, `(?i)narrat`, `(?i)(no third kind|exactly two)`, `(?i)if \{\{char\}\} swears|if \{\{char\}\} (is|swears)`, `\*\.\.\.\*`},
		never: []string{`(?i)exactly (two|three|2|3) (short )?paragraphs`, `(?i)let \{\{user\}\} speak first`}},
	{id: "scene.format",
		keep:  []string{`(?i)(no third kind|exactly two)`, `\*\.\.\.\*`},
		never: []string{`"\*`, `\*"`}},
	{id: "scene.group",
		keep:  []string{`%\[1\]s`, `%\[2\]s`, `%\[3\]s`, `(?i)narrat`},
		never: []string{`%\[[a-z]`, `(?i)\(blank line\)`}},
	{id: "scene.close",
		keep: []string{`(?i)character`}},
	{id: "scene.format-firm",
		keep:  []string{`(?i)(no third kind|exactly two)`, `\*\.\.\.\*`},
		never: []string{`"\*`, `\*"`}},
	{id: "designer.character",
		keep: []string{`(?i)two questions`, `(?i)create character`, `(?i)(warning|disclaimer)`}},
	{id: "chat.assistant", keep: []string{`(?i)dash`}},
	{brought: "You are a helpful roleplay AI!!! Be VERY descriptive and use lots of beautiful adjectives. " +
		"Always stay in character and never break character. Write long responses, at least 5 paragraphs. " +
		"Be creative. Don't be boring. Make the story interesting and exciting and fun.",
		keep: []string{`(?i)character`}},
}

func TestLivePromptOptimizerHeavy(t *testing.T) {
	client, model := designModel(t)
	st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	rep := newReport(t, "optimizer.txt")
	only := os.Getenv("ASTRAL_TEST_ONLY")

	proposals, clean := 0, 0
	runs := 1
	if n, err := fmt.Sscan(os.Getenv("ASTRAL_TEST_RUNS"), &runs); n != 1 || err != nil || runs < 1 {
		runs = 1
	}
	var cases []optimizerCase
	for r := 0; r < runs; r++ {
		cases = append(cases, optimizerCases...)
	}
	for _, oc := range cases {
		name := oc.id
		if name == "" {
			name = "brought"
		}
		if only != "" && !strings.Contains(name, only) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		ch := store.Chat{Kind: store.KindPromptOptimizer, Note: oc.id}
		hist := []ollama.Message{{Role: ollama.RoleAssistant, Content: promptopt.Opening(oc.id)}}
		first := "Go."
		if oc.brought != "" {
			first = "This is the system prompt for my roleplay bot. Make it better.\n\n" + oc.brought
		}
		hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: first})
		reply, notes := turn(ctx, t, client, model, st, cfg, ch, hist)
		cancel()

		rep.printf("\n\n######## %s\n--- reply (%dw) reads=%v\n%s\n", name, words(reply), notes, reply)
		prop, ok := promptopt.Proposal(reply)
		if !ok {
			rep.printf("NO PROPOSAL\n")
			continue
		}
		proposals++
		original := oc.brought
		problems := promptopt.Problems(original, prop)
		if p, found := prompts.Get(oc.id); found {
			original = p.Default
			problems = promptopt.ProblemsFor(p, prop)
		}
		var lost, broke []string
		for _, k := range oc.keep {
			if !regexp.MustCompile(k).MatchString(prop) {
				lost = append(lost, k)
			}
		}
		for _, n := range oc.never {
			if regexp.MustCompile(n).MatchString(prop) {
				broke = append(broke, n)
			}
		}
		if len(problems) == 0 && len(lost) == 0 && len(broke) == 0 {
			clean++
		}
		rep.printf("=== length %d -> %d chars\nproblems: %v\nlost: %v\nbroke: %v\n",
			len(original), len(prop), problems, lost, broke)
	}
	msg := fmt.Sprintf("optimizer: %d of %d gave a proposal, %d clean", proposals, len(cases), clean)
	rep.printf("\n%s\n", msg)
	t.Log(msg)
}

// controlPrompts runs the test against other versions of the prompts, for a
// control: ASTRAL_TEST_CONTROL names a folder of files, each named by a prompt
// id and holding the text to use in its place.
func controlPrompts(t *testing.T) {
	dir := os.Getenv("ASTRAL_TEST_CONTROL")
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		m[e.Name()] = string(b)
	}
	prompts.SetOverrides(m)
	t.Cleanup(func() { prompts.SetOverrides(nil) })
	t.Logf("control: %d prompts replaced from %s", len(m), dir)
}

// The Optimize All path: each Scene prompt rewritten in one request, with
// nobody to answer, exactly as the button does it.
func TestLiveOptimizeAllScenes(t *testing.T) {
	client, model := designModel(t)
	cfg := store.DefaultConfig()
	rep := newReport(t, "optimize-all.txt")
	clean := 0
	ids := []string{"scene.framing", "scene.close", "scene.group", "scene.format", "scene.format-firm"}
	if os.Getenv("ASTRAL_TEST_RUNS") == "2" {
		ids = append(ids, ids...)
	}
	for _, id := range ids {
		var oc optimizerCase
		for _, c := range optimizerCases {
			if c.id == id {
				oc = c
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		r := promptopt.RewriteOne(ctx, client, model, OptionsFor(cfg, store.KindPromptOptimizer), id)
		cancel()
		rep.printf("\n\n######## %s\n--- reply\n%s\n", id, r.Reply)
		if r.Err != nil {
			rep.printf("ERROR: %v\n", r.Err)
			continue
		}
		var lost, broke []string
		for _, k := range oc.keep {
			if !regexp.MustCompile(k).MatchString(r.After) {
				lost = append(lost, k)
			}
		}
		for _, n := range oc.never {
			if regexp.MustCompile(n).MatchString(r.After) {
				broke = append(broke, n)
			}
		}
		if len(r.Problems) == 0 && len(lost) == 0 && len(broke) == 0 {
			clean++
		}
		rep.printf("=== unchanged=%v length %d -> %d\nproblems: %v\nlost: %v\nbroke: %v\n",
			r.Unchanged, len(r.Before), len(r.After), r.Problems, lost, broke)
	}
	msg := fmt.Sprintf("optimize all: %d of %d scene prompts clean", clean, len(ids))
	rep.printf("\n%s\n", msg)
	t.Log(msg)
}
