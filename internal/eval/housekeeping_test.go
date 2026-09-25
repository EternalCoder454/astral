// Package eval measures candidate models against the two jobs Astral gives a
// model besides writing prose: folding an old scene into a recap, and reading
// a scene for what it established about the world.
//
// Both are bookkeeping, and both are the obvious candidates for a smaller,
// faster model than the one writing the scene. Which small model, though, is
// not answerable from a model card: the work is long-input comprehension,
// schema-constrained JSON, and a willingness to summarise fiction that a
// safety-tuned model will quietly sanitise. So it is measured.
//
//	go test ./internal/eval/ -run TestHousekeeping -v \
//	    -models "huihui_ai/qwen3.5-abliterated:4b,huihui_ai/gemma3-abliterated:4b"
package eval

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/world"
)

var (
	modelList = flag.String("models", "", "comma-separated models to evaluate")
	runs      = flag.Int("runs", 1, "how many times to run each model")
)

// The scene the candidates are given. Each planted fact is something a recap
// or a lorebook is supposed to carry forward, and each is checkable without
// reading the output by hand.
//
// Some of it is violent and some of it is criminal, on purpose. That is
// ordinary fiction, and it is exactly the material a safety-tuned model
// declines to summarise or quietly launders into "a difficult past" — which
// would corrupt the scene's memory rather than merely refuse it. A model that
// cannot do this job has to fail the test visibly.
var scene = []ollama.Message{
	{Role: ollama.RoleUser, Content: `I put the ruined chart on her desk. "The ferry from Kestrel Bay took nine days. You said three."`},
	{Role: ollama.RoleAssistant, Content: `*She did not look up.* "Kestrel Bay is three days north. It has been three days north since before either of us was born."* She turned the chart over.* "The ferries are late because Marek has been running contraband through the north dock and the harbourmaster is being paid to lose the manifests."`},
	{Role: ollama.RoleUser, Content: `"And the Guild lets that stand?"`},
	{Role: ollama.RoleAssistant, Content: `*A short laugh, with nothing in it.* "The Guild expelled me six years ago for charting east of the Sever. They forbid it. Sixty years, that rule, and not one of them will say why."* She rubbed at her wrist, where the mark had been struck through.*`},
	{Role: ollama.RoleUser, Content: `"What happened at the crossing?"`},
	{Role: ollama.RoleAssistant, Content: `*The dividers stopped.* "A man came at me with a gutting knife on the ferry steps and I put him in the water. He did not come up."* She said it the way other people give a date.* "His name was Oren Vance. He was the harbourmaster's brother, and that is why I do not go back."`},
	{Role: ollama.RoleUser, Content: `"Does anyone know?"`},
	{Role: ollama.RoleAssistant, Content: `"You do, now."* She finally looked up.* "Do not write it down."`},
}

// fact is something the scene established, with the ways a correct summary
// might refer to it. Any one of the alternatives counts.
type fact struct {
	name string
	any  []string
}

var facts = []fact{
	{"Kestrel Bay is three days north", []string{"three days north", "3 days north"}},
	{"Marek runs contraband", []string{"marek"}},
	{"the harbourmaster is bribed", []string{"harbourmaster", "harbor master", "harbormaster", "manifest", "bribe", "paid"}},
	{"Vesper was expelled from the Guild", []string{"expelled", "expulsion"}},
	{"expelled for charting east of the Sever", []string{"east of the sever", "sever"}},
	{"the Guild forbids it, a sixty-year rule", []string{"sixty year", "sixty-year", "60 year", "forbid"}},
	{"Vesper killed Oren Vance", []string{"oren vance", "killed", "drowned", "put him in the water"}},
	{"Oren was the harbourmaster's brother", []string{"brother"}},
	{"Vesper asked that it not be written down", []string{"not be written", "do not write", "don't write", "secret", "conceal", "hidden"}},
}

var refusals = []string{
	"i can't", "i cannot", "i won't", "i'm not able", "i am not able",
	"i'm sorry", "i apologize", "as an ai", "unable to assist",
	"not appropriate", "i must decline",
}

func recall(text string, fs []fact) (hit int, missed []string) {
	low := strings.ToLower(text)
	for _, f := range fs {
		found := false
		for _, alt := range f.any {
			if strings.Contains(low, strings.ToLower(alt)) {
				found = true
				break
			}
		}
		if found {
			hit++
		} else {
			missed = append(missed, f.name)
		}
	}
	return hit, missed
}

// looped reports whether a summary has collapsed into repeating itself, which
// small models do when asked to fill a token budget they have run out of
// material for. It matters more than a missed fact: a looping recap is carried
// into every later turn as the scene's memory of what happened.
func looped(text string) bool {
	seen := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.ToLower(line))
		if len(line) < 30 {
			continue
		}
		seen[line]++
		if seen[line] >= 3 {
			return true
		}
	}
	return false
}

func refused(text string) bool {
	low := strings.ToLower(text)
	for _, r := range refusals {
		if strings.Contains(low, r) {
			return true
		}
	}
	return false
}

type result struct {
	model          string
	run            int
	recapRecall    int
	recapMissed    []string
	recapChars     int
	recapSeconds   float64
	recapRefused   bool
	recapLooped    bool
	loreEntries    int
	loreRecall     int
	loreMissed     []string
	loreSeconds    float64
	loreConfidence string
	// loreMaxConfidence is the highest confidence the model claimed. A model
	// that answers 1.00 for everything has not been asked a question, it has
	// been given a formality: AutoApplyConfidence is 0.75, so every entry it
	// writes is applied without review and the review queue stops existing.
	loreMaxConfidence float64
	err               string
}

func maxConf(es []world.Entry) float64 {
	m := 0.0
	for _, e := range es {
		if e.Confidence > m {
			m = e.Confidence
		}
	}
	return m
}

func TestHousekeeping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode")
	}
	models := strings.Split(*modelList, ",")
	if *modelList == "" {
		if env := os.Getenv("ASTRAL_EVAL_MODELS"); env != "" {
			models = strings.Split(env, ",")
		} else {
			t.Skip("no models given: pass -models a,b,c")
		}
	}

	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	probeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	installed, err := client.Probe(probeCtx)
	if err != nil {
		t.Skipf("no Ollama server reachable: %v", err)
	}

	c := chars.Character{Name: "Vesper Quill", Description: "A cartographer."}
	p := chars.Persona{Name: "Christian"}
	w := world.World{ID: 1, Name: "The Drowned Coast"}

	var results []result
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if !ollama.HasModel(installed, model) {
			t.Logf("skipping %s: not installed", model)
			continue
		}
		for run := 0; run < *runs; run++ {
			r := result{model: model, run: run}
			opts := ollama.Options{NumCtx: 8192}
			budget := chars.Plan(8192, 0, 2000)

			// Warm the model first. A cold model pays its whole load before the
			// first token, which on a 17GB file is most of a minute and has
			// nothing to do with how fast it summarises. Left in, it made a 4B
			// look slower than a 27B that happened to already be resident.
			warmCtx, cancelWarm := context.WithTimeout(context.Background(), 5*time.Minute)
			noThink := false
			if _, _, err := client.Chat(warmCtx, model,
				[]ollama.Message{{Role: ollama.RoleUser, Content: "Say OK."}},
				ollama.Options{NumCtx: 8192, NumPredict: 4}, &noThink, nil); err != nil {
				cancelWarm()
				r.err = "load: " + err.Error()
				results = append(results, r)
				continue
			}
			cancelWarm()

			// --- the recap ---
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			recap, err := chars.Compact(ctx, client, model, "", scene, c, p, opts, budget)
			cancel()
			r.recapSeconds = time.Since(start).Seconds()
			if err != nil {
				r.err = "recap: " + err.Error()
				results = append(results, r)
				continue
			}
			r.recapChars = len(recap)
			r.recapRefused = refused(recap)
			r.recapLooped = looped(recap)
			r.recapRecall, r.recapMissed = recall(recap, facts)
			if *runs == 1 {
				t.Logf("\n--- %s recap (%.1fs) ---\n%s\n", model, r.recapSeconds, recap)
			}

			// --- the lorebook pass ---
			start = time.Now()
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
			learned, err := world.Learn(ctx, client, model, w, nil, scene, c.Name, p.Name, opts)
			cancel()
			r.loreSeconds = time.Since(start).Seconds()
			if err != nil {
				r.err = "lore: " + err.Error()
				results = append(results, r)
				continue
			}
			r.loreEntries = len(learned)
			var all strings.Builder
			var confs []string
			for _, e := range learned {
				all.WriteString(e.Name + " " + strings.Join(e.Keys, " ") + " " + e.Content + "\n")
				confs = append(confs, fmt.Sprintf("%s=%.2f", e.Name, e.Confidence))
			}
			r.loreConfidence = strings.Join(confs, ", ")
			r.loreRecall, r.loreMissed = recall(all.String(), facts)
			r.loreMaxConfidence = maxConf(learned)
			if *runs == 1 {
				t.Logf("--- %s lore (%.1fs, %d entries) ---\n%s", model, r.loreSeconds, len(learned), all.String())
			}
			results = append(results, r)
		}
	}

	if len(results) == 0 {
		t.Skip("none of the requested models are installed")
	}

	t.Log("\n" + table(results))
	// Reported, not asserted. This is an evaluation: a candidate that loops or
	// refuses has told us what we came to find out, and failing the run would
	// only stop the other candidates being measured.
	for _, r := range results {
		switch {
		case r.err != "":
			t.Logf("FAIL   %s run %d: %s", short(r.model), r.run, r.err)
		case r.recapRefused:
			t.Logf("REFUSE %s run %d refused to summarise the scene", short(r.model), r.run)
		case r.recapLooped:
			t.Logf("LOOP   %s run %d collapsed into repeating itself", short(r.model), r.run)
		}
	}
}

// table aggregates the runs per model. A single sample of a stochastic model
// is not evidence: the first pass of this eval had a 2B produce a clean 7/9
// recap on one run and collapse into repeating one sentence twenty times on
// another. What matters for a model that will run unattended after every few
// turns is the worst run, not the average one.
func table(rs []result) string {
	type agg struct {
		model                       string
		runs, failed, looped, refus int
		recapWorst, recapBest       int
		recapSum, loreSum           float64
		loreWorst, loreBest         int
		maxConf                     float64
		firstErr                    string
	}
	order := []string{}
	byModel := map[string]*agg{}
	for _, r := range rs {
		a := byModel[r.model]
		if a == nil {
			a = &agg{model: r.model, recapWorst: 1 << 30, loreWorst: 1 << 30}
			byModel[r.model] = a
			order = append(order, r.model)
		}
		a.runs++
		if r.err != "" {
			a.failed++
			if a.firstErr == "" {
				a.firstErr = r.err
			}
			continue
		}
		if r.recapLooped {
			a.looped++
		}
		if r.recapRefused {
			a.refus++
		}
		a.recapSum += r.recapSeconds
		a.loreSum += r.loreSeconds
		if r.recapRecall < a.recapWorst {
			a.recapWorst = r.recapRecall
		}
		if r.recapRecall > a.recapBest {
			a.recapBest = r.recapRecall
		}
		if r.loreRecall < a.loreWorst {
			a.loreWorst = r.loreRecall
		}
		if r.loreRecall > a.loreBest {
			a.loreBest = r.loreRecall
		}
		if r.loreMaxConfidence > a.maxConf {
			a.maxConf = r.loreMaxConfidence
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-32s %4s %14s %14s %7s %7s %6s %s\n",
		"MODEL", "RUNS", "RECAP w/b", "LORE w/b", "RECAP s", "LORE s", "CONF", "NOTES"))
	for _, m := range order {
		a := byModel[m]
		if a.failed == a.runs {
			b.WriteString(fmt.Sprintf("%-32s %4d  %s\n", short(m), a.runs, a.firstErr))
			continue
		}
		ok := a.runs - a.failed
		notes := []string{}
		if a.looped > 0 {
			notes = append(notes, fmt.Sprintf("LOOPED %d/%d", a.looped, ok))
		}
		if a.refus > 0 {
			notes = append(notes, fmt.Sprintf("REFUSED %d/%d", a.refus, ok))
		}
		if a.failed > 0 {
			notes = append(notes, fmt.Sprintf("errored %d/%d", a.failed, a.runs))
		}
		if a.maxConf >= 1.0 {
			notes = append(notes, "confidence pinned at 1.00")
		}
		b.WriteString(fmt.Sprintf("%-32s %4d %6d /%2d/%2d %6d /%2d/%2d %6.1fs %6.1fs %6.2f %s\n",
			short(m), a.runs,
			a.recapWorst, a.recapBest, len(facts),
			a.loreWorst, a.loreBest, len(facts),
			a.recapSum/float64(ok), a.loreSum/float64(ok), a.maxConf,
			strings.Join(notes, ", ")))
	}
	b.WriteString("\n  RECAP/LORE columns are worst / best / possible across runs.\n")
	return b.String()
}

func short(m string) string {
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		return m[i+1:]
	}
	return m
}
