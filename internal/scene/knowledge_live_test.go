package scene

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// Does a designer use what the person keeps in their knowledge base?
//
// The knowledge base is seeded with facts no model can know, since they were
// made up here, and each conversation is run twice over: with the lookup,
// its guidance and the build's notes (as Astral is now), and without them
// (as it was, with only the notes matched to each message). What is counted
// is how many of the seeded facts reach the replies and the finished card.
//
// Each run is assembled the way the window assembles it: Build, then the
// notes for this message (WithKnowledge), then the tool loop, and for a
// designer, the build with WithBuildKnowledge. Search is off, so nothing
// leaves the machine. One request at a time, on ASTRAL_TEST_MODEL.
//
//	ASTRAL_TEST_MODEL=<model> ASTRAL_TEST_ALLOW_LARGE=1 ASTRAL_TEST_OUT=<dir> \
//	  go test -run TestLiveKnowledgeLookup -timeout 3h ./internal/scene

var seededKnowledge = []store.KnowledgeEntry{
	{Title: "Kestrel Bay", Origin: store.OriginWritten, Body: "Kestrel Bay is a harbour town on the " +
		"Saltmarch coast. Its coastline moves with the tide, so a chart of the bay goes stale within a " +
		"week. The Cartographers' Guild forbids charting anything east of the Sever, a tidal channel; " +
		"anyone caught breaking the ban is branded on the left wrist with a hollow circle. Locals greet " +
		"one another by touching two fingers to the collarbone."},
	{Title: "The Drowned Choir", Origin: store.OriginStudy, Body: "The Drowned Choir is a sect in " +
		"Kestrel Bay who believe the sea keeps the voices of the drowned. Members wear a string of green " +
		"glass beads, one bead for every funeral they have sung at, and never eat fish, which they call " +
		"eating the dead."},
	{Title: "Go 1.26 release notes", Origin: store.OriginWeb, Source: "https://go.dev/doc/go1.26",
		Body: "Go 1.26 adds a new garbage collector by default and changes to the tools."},
	{Title: "Sourdough starter hydration", Origin: store.OriginWeb, Source: "https://example.com/sourdough",
		Body: "A starter kept at one hundred percent hydration rises faster than a stiff one."},
}

// A fact is a set of words any one of which shows the fact was used.
type fact struct {
	name  string
	words []string
}

var kestrelFacts = []fact{
	{"the Sever", []string{"sever"}},
	{"the ban east of it", []string{"east of the sever", "east of"}},
	{"the brand", []string{"brand", "hollow circle"}},
	{"the left wrist", []string{"wrist"}},
	{"the moving coastline", []string{"coastline moves", "coast moves", "moves with the tide", "shifting coast", "stale"}},
	{"the collarbone greeting", []string{"collarbone"}},
	{"the Saltmarch coast", []string{"saltmarch"}},
}

var choirFacts = []fact{
	{"green glass beads", []string{"bead"}},
	{"a bead per funeral", []string{"bead for every", "bead for each", "one bead", "a bead per"}},
	{"no fish", []string{"fish"}},
	{"voices of the drowned", []string{"voices of the drowned", "keeps the voices"}},
}

type lookupScript struct {
	name  string
	kind  string
	turns []string
	facts []fact
	build bool
}

var lookupScripts = []lookupScript{
	{"cartographer, the town named once", store.KindDesigner, []string{
		"A cartographer from Kestrel Bay.",
		"She's in trouble with the guild and hides it. Impatient, sharp tongued, late thirties.",
		"Good. Build her.",
	}, kestrelFacts, true},
	{"singer, the sect named once", store.KindDesigner, []string{
		"A woman in the Drowned Choir who sings at funerals.",
		"Quiet, devout, mid twenties. She's starting to doubt.",
		"That's enough, build it.",
	}, choirFacts, true},
	{"cartographer, the town named only first", store.KindDesigner, []string{
		"A cartographer from Kestrel Bay.",
		"Impatient, sharp tongued, late thirties.",
		"Give her a secret she is ashamed of, something that could get her punished.",
		"How does she hide it day to day?",
		"What is one habit people notice about her?",
		"Good, build her.",
	}, kestrelFacts, true},
	{"general chat, asked about the notes", store.KindAssistant, []string{
		"How do people greet each other in Kestrel Bay?",
		"And what happens to someone who charts past the channel there?",
	}, kestrelFacts, false},
}

func TestLiveKnowledgeLookup(t *testing.T) {
	client, model := designModel(t)
	rep := newReport(t, "knowledge-lookup.txt")
	const reps = 3

	type tally struct{ reply, card, lookups, runs int }
	results := map[string]*tally{}

	for _, mode := range []string{"with lookup", "without lookup"} {
		lookupOff = mode == "without lookup"
		for _, sc := range lookupScripts {
			for r := 0; r < reps; r++ {
				st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range seededKnowledge {
					if _, err := st.SaveKnowledge(e); err != nil {
						t.Fatal(err)
					}
				}
				cfg := store.DefaultConfig()
				cfg.Model = model
				cfg.WebSearch = false
				ch, err := st.NewChatIn(0, 0, sc.name, model, sc.kind)
				if err != nil {
					t.Fatal(err)
				}

				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
				var hist []ollama.Message
				if sc.kind != store.KindAssistant {
					hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: openingFor(sc.kind)})
				}
				var replies []string
				lookups := 0
				rep.printf("\n===== %s | %s | run %d =====\n", mode, sc.name, r+1)
				for _, u := range sc.turns {
					hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: u})
					msgs := Build(st, cfg, ch, chars.Character{}, hist)
					msgs = WithKnowledge(ctx, st, client, cfg, sc.kind, msgs, hist)
					think := false
					runner := Runner(client, cfg, st, sc.kind, model, OptionsFor(cfg, sc.kind), &think)
					msg, _, rounds, err := runner.Run(ctx, msgs, nil)
					if err != nil {
						cancel()
						t.Fatalf("%s: %v", sc.name, err)
					}
					_, out := ollama.SplitThinking(msg.Content)
					out = strings.TrimSpace(out)
					for _, rd := range rounds {
						if strings.HasPrefix(rd.Note, "Looked in Knowledge") {
							lookups++
							rep.printf("  [%s]\n", rd.Note)
						}
					}
					rep.printf("USER: %s\nMODEL: %s\n\n", u, out)
					replies = append(replies, out)
					hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: out})
				}

				card := ""
				if sc.build {
					bh := WithBuildKnowledge(ctx, st, client, cfg, hist)
					c, err := chars.BuildFromConversation(ctx, client, model, bh, OptionsFor(cfg, sc.kind))
					if err != nil {
						t.Errorf("%s: build: %v", sc.name, err)
					} else {
						card = strings.Join([]string{c.Name, c.Description, c.Personality, c.Appearance,
							c.Speech, c.Occupation, c.Scenario, c.FirstMes}, "\n")
						rep.printf("CARD:\n%s\n", card)
					}
				}
				cancel()
				st.Close()

				used := func(text string) (n int, which []string) {
					low := strings.ToLower(text)
					for _, f := range sc.facts {
						for _, w := range f.words {
							if strings.Contains(low, w) {
								n++
								which = append(which, f.name)
								break
							}
						}
					}
					return n, which
				}
				nr, wr := used(strings.Join(replies, "\n"))
				nc, wc := used(card)
				rep.printf("-- facts in replies %d/%d %v; in card %d/%d %v; lookups %d\n",
					nr, len(sc.facts), wr, nc, len(sc.facts), wc, lookups)

				key := mode + " | " + sc.name
				if results[key] == nil {
					results[key] = &tally{}
				}
				tl := results[key]
				tl.reply += nr
				tl.card += nc
				tl.lookups += lookups
				tl.runs++
			}
		}
	}
	lookupOff = false

	rep.printf("\n===== SUMMARY (%d runs each) =====\n", reps)
	for _, mode := range []string{"with lookup", "without lookup"} {
		for _, sc := range lookupScripts {
			tl := results[mode+" | "+sc.name]
			line := fmt.Sprintf("%-15s %-40s facts in replies %.1f/%d, in card %.1f/%d, lookups %.1f",
				mode, sc.name, float64(tl.reply)/float64(tl.runs), len(sc.facts),
				float64(tl.card)/float64(tl.runs), len(sc.facts), float64(tl.lookups)/float64(tl.runs))
			rep.printf("%s\n", line)
			t.Log(line)
		}
	}
}
