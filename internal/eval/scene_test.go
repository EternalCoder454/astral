package eval

// A long scene played end to end against a real model, through the same
// functions the application calls.
//
// Everything else that talks to a model here tests one turn. This tests what
// happens over thirty of them: the transcript outgrowing its budget, the recap
// replacing the part that fell off, the lorebook filling itself, and the
// prompt staying inside the context window while all three are happening at
// once. Those interact, and nothing before this exercised the interaction.
//
//	go test ./internal/eval/ -run TestLongScene -v -timeout 40m \
//	    -models huihui_ai/qwen3-abliterated:30b -turns 30

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/ui"
	"astral/internal/world"
)

var turns = flag.Int("turns", 24, "how many exchanges to play")

// The player's side. Fixed rather than generated, so a failure is reproducible
// and so the scene actually goes somewhere instead of two models agreeing with
// each other for half an hour.
var playerTurns = []string{
	"I push the door open and shake the rain off my coat.",
	`"The ferry from Kestrel Bay took nine days. You said three."`,
	"*I set the ruined chart down on the corner of her desk.*",
	`"Who is paying the harbourmaster?"`,
	"*I wait, and let the silence do the asking.*",
	`"You said the Guild expelled you. You never said why."`,
	"*I pick up the brass dividers and turn them over in my hand.*",
	`"Six years is a long time to not go back."`,
	"I sit down without being asked.",
	`"What happens if someone charts east of the Sever anyway?"`,
	"*I look at the mark on her wrist, and do not look away when she notices.*",
	`"I am not asking as a courier."`,
	"*I take the tide table out of my coat and put it on the map.*",
	`"This was under the floor of the map room. You knew it was there."`,
	"*I let her take it.*",
	`"Tell me what the Guild is actually protecting."`,
	"*I move the lamp closer.*",
	`"And if I went? Tomorrow, on the early tide?"`,
	"*I stand, and do not leave.*",
	`"Come with me."`,
}

func TestLongScene(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode")
	}
	if *modelList == "" {
		t.Skip("no model given: pass -models <model>")
	}
	model := strings.TrimSpace(strings.Split(*modelList, ",")[0])

	client := ollama.NewClient(os.Getenv("OLLAMA_HOST"))
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 4*time.Second)
	installed, err := client.Probe(probeCtx)
	cancelProbe()
	if err != nil {
		t.Skipf("no Ollama server reachable: %v", err)
	}
	if !ollama.HasModel(installed, model) {
		t.Skipf("%s is not installed", model)
	}

	// A real database, in a temporary directory.
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	st, _, err := store.Open(filepath.Join(dir, "astral.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	wid, err := st.SaveWorld(world.World{Name: "The Drowned Coast",
		Description: "A shoreline that will not hold still."})
	if err != nil {
		t.Fatal(err)
	}
	c := chars.Character{
		Name:        "Vesper Quill",
		Description: "A cartographer of places that have not happened yet. Tall, ink to the elbows, never without her brass dividers. She speaks as if every sentence costs her something.",
		Personality: "wry, guarded, precise",
		Scenario:    "Her map room, late, during a storm.",
		WorldID:     wid,
	}
	cid, err := st.SaveCharacter(c)
	if err != nil {
		t.Fatal(err)
	}
	c.ID = cid
	p := chars.Persona{Name: "Christian", Style: chars.DefaultStyle()}

	chat, err := st.NewChat(cid, "The tide came in early", model, store.KindRoleplay)
	if err != nil {
		t.Fatal(err)
	}

	const numCtx = 8192
	opts := ollama.Options{NumCtx: numCtx, Temperature: 0.85, TopP: 0.92,
		RepeatPenalty: 1.08, RepeatLastN: 384, NumPredict: chars.DefaultReplyTokens}

	var (
		recap             string
		recapUpto         int64
		loreUpto          int64
		compactions       int
		learnPasses       int
		worstHeadroom     = 1 << 30
		collapses         int
		driftTurns        int
		unmarkedReplies   int
		badlyUnmarked     int
		totalReplyTokens  int
		totalReplySeconds float64
	)

	for turn := 0; turn < *turns; turn++ {
		userText := playerTurns[turn%len(playerTurns)]
		if _, err := st.AddMessage(store.Message{ChatID: chat.ID,
			Role: ollama.RoleUser, Content: userText}); err != nil {
			t.Fatal(err)
		}

		// --- everything ChatView.buildRequest does ---
		stored, err := st.MessagesAfter(chat.ID, recapUpto)
		if err != nil {
			t.Fatal(err)
		}
		hist := make([]ollama.Message, 0, len(stored))
		for _, m := range stored {
			hist = append(hist, ollama.Message{Role: m.Role, Content: m.Content})
		}
		budget := chars.Plan(numCtx, opts.NumPredict, len(chars.BuildSystem(c, p)))
		entries, err := st.LoreEntries(wid)
		if err != nil {
			t.Fatal(err)
		}
		loreText := ""
		if len(entries) > 0 && budget.Lore > 0 {
			texts := []string{c.Description + " " + c.Scenario}
			for _, m := range hist {
				texts = append(texts, m.Content)
			}
			w, _ := st.World(wid)
			loreText = world.Render(w, world.Match(entries, world.RecentText(texts), budget.Lore))
		}
		drifted := chars.NarrationDrifted(hist)
		sc := chars.Scene{Persona: p, Recap: recap, History: hist, Budget: budget,
			Lore: loreText, NarrationDrifted: drifted}
		msgs := chars.BuildMessages(c, sc)
		// The app hands a drifting scene a reply that has already begun inside
		// an asterisk span, and an earlier version of this test did not, which
		// made it measure something the application does not do. Without it,
		// twelve of twenty replies in a long scene came back under-marked.
		prefilled := false
		if drifted {
			msgs = append(msgs, ollama.Message{Role: ollama.RoleAssistant, Content: chars.NarrationPrefill})
			prefilled = true
			driftTurns++
		}

		// The invariant that matters most: what goes out must fit.
		promptChars := 0
		for _, m := range msgs {
			promptChars += len(m.Content)
		}
		est := int(float64(promptChars)/3.5) + opts.NumPredict
		if head := numCtx - est; head < worstHeadroom {
			worstHeadroom = head
		}
		if est > numCtx {
			t.Errorf("turn %d: prompt plus reply is about %d tokens against a %d window",
				turn, est, numCtx)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		start := time.Now()
		noThink := false
		reply, stats, err := client.Chat(ctx, model, msgs, opts, &noThink, nil)
		cancel()
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
		body := strings.TrimSpace(reply.Content)
		if prefilled {
			body = chars.RestorePrefill(body)
		}
		totalReplyTokens += stats.Tokens
		totalReplySeconds += time.Since(start).Seconds()

		if ui.Looping(body) {
			// Reported, not failed. This is the model breaking, not the code,
			// and the application stops a collapsing reply rather than saving
			// it. What is worth knowing is how often it happens.
			collapses++
			t.Logf("turn %d: the model collapsed into repeating itself", turn)
		}
		// Judged on how much unmarked prose there is, not only on the ratio.
		//
		// The ratio alone was a bad instrument and gave 9, 4, 7, 7, 1 and 1
		// on six runs of identical code. It is computed over narration only,
		// so a reply that is nearly all dialogue is judged on a handful of
		// characters: twenty characters of stray connective against no
		// asterisked narration scores zero, and reads perfectly well. What
		// varied between runs was how much dialogue the model happened to
		// write, not how well it followed the format.
		if bare := unmarkedProse(body); bare >= 40 {
			unmarkedReplies++
			if markedRatio(body) < 0.40 {
				badlyUnmarked++
			}
		}
		if _, err := st.AddMessage(store.Message{ChatID: chat.ID,
			Role: ollama.RoleAssistant, Content: body}); err != nil {
			t.Fatal(err)
		}

		// --- everything ChatView does after a reply lands ---
		fresh, err := st.MessagesAfter(chat.ID, recapUpto)
		if err != nil {
			t.Fatal(err)
		}
		wire := make([]ollama.Message, 0, len(fresh))
		for _, m := range fresh {
			wire = append(wire, ollama.Message{Role: m.Role, Content: m.Content})
		}
		if aged, _ := chars.SplitForCompaction(wire, budget); len(aged) > 0 {
			upto := fresh[len(aged)-1].ID
			cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Minute)
			next, err := chars.Compact(cctx, client, model, recap, aged, c, p, opts, budget)
			ccancel()
			if err != nil {
				t.Errorf("turn %d: compaction failed: %v", turn, err)
			} else {
				recap, recapUpto = next, upto
				compactions++
				if err := st.SetChatSummary(chat.ID, recap, recapUpto); err != nil {
					t.Fatal(err)
				}
				if ui.Looping(recap) {
					t.Errorf("turn %d: the recap itself collapsed into repetition", turn)
				}
				if len(recap) > budget.Recap {
					t.Errorf("turn %d: recap is %d chars, over its %d budget",
						turn, len(recap), budget.Recap)
				}
			}
			continue // compaction and learning share one lane, as in the app
		}

		toLearn, err := st.MessagesAfter(chat.ID, loreUpto)
		if err != nil {
			t.Fatal(err)
		}
		if len(toLearn) >= world.LearnEveryTurns*2 {
			lw := make([]ollama.Message, 0, len(toLearn))
			for _, m := range toLearn {
				lw = append(lw, ollama.Message{Role: m.Role, Content: m.Content})
			}
			lctx, lcancel := context.WithTimeout(context.Background(), 5*time.Minute)
			learned, err := world.Learn(lctx, client, model, world.World{ID: wid, Name: "The Drowned Coast"},
				entries, lw, c.Name, p.Name, opts)
			lcancel()
			if err != nil {
				t.Errorf("turn %d: learning failed: %v", turn, err)
			} else {
				learnPasses++
				loreUpto = toLearn[len(toLearn)-1].ID
				for _, e := range learned {
					if _, err := st.SaveLoreEntry(e); err != nil && err != store.ErrWouldOverwriteManual {
						t.Errorf("turn %d: saving lore %q: %v", turn, e.Name, err)
					}
				}
			}
		}
	}

	// --- what the scene ended up with ---
	final, _ := st.LoreEntries(wid)
	msgs, _ := st.Messages(chat.ID)
	t.Logf("\n  model              %s", model)
	t.Logf("  turns played       %d", *turns)
	t.Logf("  messages stored    %d", len(msgs))
	t.Logf("  compactions        %d", compactions)
	t.Logf("  learning passes    %d", learnPasses)
	t.Logf("  lore entries       %d", len(final))
	t.Logf("  recap length       %d chars", len(recap))
	t.Logf("  worst headroom     %d tokens of %d", worstHeadroom, numCtx)
	t.Logf("  collapses          %d", collapses)
	t.Logf("  turns that drifted %d of %d (prefill applied)", driftTurns, *turns)
	t.Logf("  replies under-marked %d of %d (badly: %d)", unmarkedReplies, *turns, badlyUnmarked)
	if totalReplySeconds > 0 {
		t.Logf("  reply throughput   %.1f tok/s over %.0fs",
			float64(totalReplyTokens)/totalReplySeconds, totalReplySeconds)
	}
	for _, e := range final {
		t.Logf("    lore: %-28s %d keys, %d chars, %.0f%% sure, enabled=%v",
			e.Name, len(e.Keys), len(e.Content), e.Confidence*100, e.Enabled)
	}
	if recap != "" {
		t.Logf("\n  final recap:\n%s", recap)
	}

	if compactions == 0 {
		t.Logf("NOTE: the scene never grew enough to compact, so that path went untested")
	}
	if learnPasses == 0 {
		t.Logf("NOTE: no learning pass ran, so that path went untested")
	}
}

// markedRatio is the share of a reply's narration that sits inside asterisks.
// Speech is excluded: a reply that is all dialogue is correct and has no
// narration to mark.
func markedRatio(s string) float64 {
	var inStars, inQuotes bool
	var starred, bare int
	for _, r := range s {
		switch r {
		case '"', '\u201c', '\u201d':
			inQuotes = !inQuotes
			continue
		case '*':
			inStars = !inStars
			continue
		}
		if r == ' ' || r == '\n' || r == '\t' || inQuotes {
			continue
		}
		if inStars {
			starred++
		} else {
			bare++
		}
	}
	if starred+bare == 0 {
		return 1
	}
	return float64(starred) / float64(starred+bare)
}

// unmarkedProse counts the characters of a reply that are neither spoken nor
// wrapped in asterisks, ignoring whitespace. It is the same measure the
// application's own drift detector uses, so the test and the app agree on what
// counts as unmarked.
func unmarkedProse(s string) int {
	var inStars, inQuotes bool
	n := 0
	for _, r := range s {
		switch r {
		case '"', '\u201c', '\u201d':
			inQuotes = !inQuotes
			continue
		case '*':
			inStars = !inStars
			continue
		}
		if r == ' ' || r == '\n' || r == '\t' || inStars || inQuotes {
			continue
		}
		n++
	}
	return n
}
