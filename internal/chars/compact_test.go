package chars

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"astral/internal/ollama"
)

func turns(n, size int) []ollama.Message {
	out := make([]ollama.Message, 0, n)
	for i := 0; i < n; i++ {
		role := ollama.RoleUser
		if i%2 == 1 {
			role = ollama.RoleAssistant
		}
		out = append(out, ollama.Message{
			Role:    role,
			Content: fmt.Sprintf("turn %d ", i) + strings.Repeat("x", size),
		})
	}
	return out
}

// testBudget is the plan these tests measure against: Astral's defaults with a
// middling character card.
func testBudget() Budget { return Plan(8192, 0, 4000) }

func TestNeedsCompaction(t *testing.T) {
	b := testBudget()
	if NeedsCompaction(turns(4, 100), b) {
		t.Error("a short scene was marked for compaction")
	}
	if !NeedsCompaction(turns(40, 1000), b) {
		t.Error("a long scene was not marked for compaction")
	}
}

func TestSplitForCompactionKeepsTheRecentHalf(t *testing.T) {
	b := testBudget()
	history := turns(60, 1000)
	aged, recent := SplitForCompaction(history, b)

	if len(aged) == 0 {
		t.Fatal("nothing was aged out of a scene well over the threshold")
	}
	if len(recent) == 0 {
		t.Fatal("the recent half is empty — there would be nothing to reply to")
	}
	if len(aged)+len(recent) != len(history) {
		t.Errorf("split lost turns: %d + %d != %d", len(aged), len(recent), len(history))
	}
	// The split must fall on a turn boundary in the right place: recent is the
	// tail, aged is the head, and they are contiguous.
	if aged[len(aged)-1].Content == recent[0].Content {
		t.Error("a turn appears on both sides of the split")
	}
	if recent[len(recent)-1].Content != history[len(history)-1].Content {
		t.Error("the newest turn was not kept verbatim")
	}
	kept := 0
	for _, m := range recent {
		kept += len(m.Content)
	}
	if kept > b.Keep+1000 {
		t.Errorf("kept %d chars verbatim, well over the %d budget", kept, b.Keep)
	}
}

func TestSplitLeavesShortScenesAlone(t *testing.T) {
	history := turns(4, 100)
	aged, recent := SplitForCompaction(history, testBudget())
	if len(aged) != 0 || len(recent) != len(history) {
		t.Errorf("a short scene was split: %d aged, %d recent", len(aged), len(recent))
	}
}

// A single turn larger than the whole verbatim budget must not produce an
// empty recent half — the model would have nothing to answer.
func TestSplitSurvivesOneEnormousTurn(t *testing.T) {
	history := []ollama.Message{
		{Role: ollama.RoleUser, Content: strings.Repeat("a", 5000)},
		{Role: ollama.RoleAssistant, Content: strings.Repeat("b", testBudget().Compact+5000)},
	}
	aged, recent := SplitForCompaction(history, testBudget())
	if len(recent) == 0 {
		t.Fatal("recent half is empty")
	}
	if len(aged)+len(recent) != len(history) {
		t.Errorf("split lost turns: %d + %d != 2", len(aged), len(recent))
	}
}

func compactServer(t *testing.T, reply string, saw func(map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if saw != nil {
			saw(req)
		}
		resp, _ := json.Marshal(map[string]any{
			"message": map[string]string{"role": "assistant", "content": reply},
			"done":    true, "done_reason": "stop", "eval_count": 50, "eval_duration": 1e9,
		})
		fmt.Fprintln(w, string(resp))
	}))
}

// The recap is rolling: each pass folds the previous record in, which is how
// detail from the start of a scene survives many compactions.
func TestCompactCarriesThePreviousRecord(t *testing.T) {
	var req map[string]any
	srv := compactServer(t, "Vesper showed Wren the map. Wren admitted he had been there before.",
		func(r map[string]any) { req = r })
	defer srv.Close()

	out, err := Compact(context.Background(), ollama.NewClient(srv.URL), "m",
		"Vesper met Wren at the map room.",
		[]ollama.Message{{Role: ollama.RoleUser, Content: "I looked at the coastline."}},
		Character{Name: "Vesper"}, Persona{Name: "Wren"}, ollama.Options{}, testBudget())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !strings.Contains(out, "admitted he had been there") {
		t.Errorf("result = %q", out)
	}

	msgs, _ := req["messages"].([]any)
	var blob strings.Builder
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok {
			blob.WriteString(fmt.Sprint(mm["content"]))
		}
	}
	for _, want := range []string{"Vesper met Wren at the map room.", "I looked at the coastline.", "Vesper", "Wren"} {
		if !strings.Contains(blob.String(), want) {
			t.Errorf("the request did not carry %q", want)
		}
	}
	// Bookkeeping, not invention.
	if opts, ok := req["options"].(map[string]any); ok {
		if temp, ok := opts["temperature"].(float64); ok && temp > 0.3 {
			t.Errorf("temperature = %v, too high for a factual record", temp)
		}
	}
}

func TestCompactWithNothingAgedIsANoOp(t *testing.T) {
	out, err := Compact(context.Background(), ollama.NewClient("http://127.0.0.1:1"), "m",
		"previous", nil, Character{Name: "V"}, Persona{Name: "Z"}, ollama.Options{}, testBudget())
	if err != nil || out != "previous" {
		t.Errorf("out=%q err=%v, want the previous record untouched", out, err)
	}
}

// A failed compaction must return the previous record rather than an empty
// one: losing the recap is worse than not updating it.
func TestCompactKeepsThePreviousRecordOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	out, err := Compact(context.Background(), ollama.NewClient(srv.URL), "m", "previous",
		[]ollama.Message{{Role: ollama.RoleUser, Content: "hi"}},
		Character{Name: "V"}, Persona{Name: "Z"}, ollama.Options{}, testBudget())
	if err == nil {
		t.Error("a server error was not reported")
	}
	if out != "previous" {
		t.Errorf("out = %q, want the previous record preserved", out)
	}
}

func TestRecapIsBounded(t *testing.T) {
	// Distinct sentences, because identical ones are now collapsed before the
	// bound is applied and this test would stop exercising the bound at all.
	long := bloatedRecord(400)
	srv := compactServer(t, long, nil)
	defer srv.Close()

	out, err := Compact(context.Background(), ollama.NewClient(srv.URL), "m", "",
		[]ollama.Message{{Role: ollama.RoleUser, Content: "hi"}},
		Character{Name: "V"}, Persona{Name: "Z"}, ollama.Options{}, testBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > testBudget().Recap {
		t.Errorf("recap is %d chars, over the %d budget", len(out), testBudget().Recap)
	}
	// Cut at a sentence end, not mid-fact.
	if !strings.HasSuffix(strings.TrimSpace(out), ".") {
		t.Errorf("recap was cut mid-sentence: %q", out[len(out)-40:])
	}
}

// The recap has to reach the model, before the transcript and marked as fact.
func TestRecapAppearsBeforeTheTranscript(t *testing.T) {
	msgs := BuildMessages(testChar(), Scene{
		Persona: Persona{Name: "Wren"},
		Recap:   "Vesper admitted she has never left the city.",
		History: []ollama.Message{{Role: ollama.RoleUser, Content: "Tell me more."}},
	})

	var recapAt, turnAt = -1, -1
	for i, m := range msgs {
		if strings.Contains(m.Content, "never left the city") {
			recapAt = i
		}
		if m.Content == "Tell me more." {
			turnAt = i
		}
	}
	if recapAt < 0 {
		t.Fatalf("the recap never reached the model:\n%+v", msgs)
	}
	if turnAt < 0 || recapAt > turnAt {
		t.Errorf("recap at %d, transcript at %d — it must come first", recapAt, turnAt)
	}
	if !strings.Contains(msgs[recapAt].Content, "established fact") {
		t.Errorf("the recap is not marked as fact:\n%s", msgs[recapAt].Content)
	}
}

// A recap is carried in the prompt on every turn for the rest of the scene, so
// a sentence the summariser wrote twice is paid for over and over. This is the
// record a measured run actually produced, abridged: the model had the facts
// and then circled them.
const circlingRecap = `Vesper Quill and Wren are characters in a scene. ` +
	`Vesper Quill admitted she has never left the city. ` +
	`Wren promised to take her after the solstice. ` +
	`Vesper Quill turned the map over, hiding the coastline. ` +
	`Wren was late because the ferry from Kestrel Bay was held. ` +
	`Vesper Quill's map had a coastline that she hid. ` +
	`Wren did not look at it yet. ` +
	`The lamp guttered. Neither of them moved to trim it. ` +
	`Vesper Quill's map had a coastline that she hid. ` +
	`Wren did not look at it yet. ` +
	`The lamp guttered. Neither of them moved to trim it. ` +
	`Vesper Quill's map had a coastline that she hid. ` +
	`Wren did not look at it yet.`

func TestDedupeRecapReclaimsWhatCirclingWasted(t *testing.T) {
	got := dedupeRecap(circlingRecap)
	if len(got) >= len(circlingRecap) {
		t.Errorf("nothing was reclaimed: %d chars in, %d out", len(circlingRecap), len(got))
	}
	// Every fact has to survive. This is the half of the job that matters:
	// a recap that loses a fact is worse than a repetitive one.
	for _, fact := range []string{
		"never left the city", "after the solstice", "hiding the coastline",
		"ferry from Kestrel Bay", "lamp guttered", "trim it",
	} {
		if !strings.Contains(got, fact) {
			t.Errorf("dedupe lost %q:\n%s", fact, got)
		}
	}
	// And each of the repeated ones appears once now.
	for _, once := range []string{"a coastline that she hid", "did not look at it yet", "The lamp guttered"} {
		if n := strings.Count(got, once); n != 1 {
			t.Errorf("%q appears %d times, want 1:\n%s", once, n, got)
		}
	}
}

func TestDedupeRecapLeavesAnHonestRecordAlone(t *testing.T) {
	clean := "Vesper admitted she has never left the city. " +
		"Wren promised to take her after the solstice. " +
		"Vesper hid the coastline side of the map."
	if got := dedupeRecap(clean); got != clean {
		t.Errorf("a record with no repeats was changed:\n in:  %s\n out: %s", clean, got)
	}
}

// A record written as a list has to still be a list: folding the bullets into
// one paragraph would change what the next model reads.
func TestDedupeRecapKeepsLines(t *testing.T) {
	in := "- Vesper has never left the city.\n- Wren promised to take her.\n- Vesper has never left the city.\n- The map is hidden."
	got := dedupeRecap(in)
	// Four lines in, one of them a repeat: three lines out, so two breaks.
	if want := 2; strings.Count(got, "\n") != want {
		t.Errorf("line breaks = %d, want %d:\n%s", strings.Count(got, "\n"), want, got)
	}
	if strings.Count(got, "never left the city") != 1 {
		t.Errorf("the repeated line survived:\n%s", got)
	}
}

// Sentence splitting must not be fooled by a decimal or an ellipsis, or a
// statement gets cut in half and its two halves are compared with everything.
func TestSplitStatementsIgnoresMidSentenceStops(t *testing.T) {
	got := splitStatements("The scale was 1.5 to the mile. She said... nothing.")
	if len(got) != 2 {
		for i, s := range got {
			t.Logf("%d: %q", i, s.text)
		}
		t.Fatalf("split into %d statements, want 2", len(got))
	}
}

// FuzzDedupeRecap runs the splitter over arbitrary text. It indexes bytes and
// slices between them, so the properties worth holding are that it never panics,
// never grows what it was given, and never invents a character.
func FuzzDedupeRecap(f *testing.F) {
	for _, seed := range []string{
		"", ".", "..", "...", "a. a. a.", "A.\nB.\nA.\n", "1.5 to the mile.",
		"Ünïcode. Ünïcode.", "no ending at all", "!?!?", "\n\n\n", " . . . ",
		"Vesper hid it. Wren did not look. Vesper hid it.",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := dedupeRecap(in)
		if len(out) > len(in) {
			t.Fatalf("dedupe grew %q into %q", in, out)
		}
		if utf8.ValidString(in) && !utf8.ValidString(out) {
			t.Fatalf("dedupe cut a rune in half: %q -> %q", in, out)
		}
		// A filter, never an editor: every statement of the input that is not a
		// repeat of an earlier one has to survive verbatim. Checking it this way
		// round rather than re-splitting the output is deliberate, because
		// dropping a punctuation-only statement can leave its two neighbours
		// adjacent, and re-splitting then reads them as one.
		seen := map[string]bool{}
		for _, st := range splitStatements(in) {
			key := recapKey(st.text)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			if !strings.Contains(out, st.text) {
				t.Fatalf("input statement %q was lost from %q\n out: %q", st.text, in, out)
			}
		}
		// Idempotent: running it again can find nothing new to drop.
		if again := dedupeRecap(out); again != out {
			t.Fatalf("not idempotent:\n1: %q\n2: %q", out, again)
		}
	})
}

// A 4B asked to rewrite a record it had just been shown reproduced the whole
// prompt instead: the record, the heading between the sections, and then the
// transcript word for word. Stored as the recap, that carries the transcript in
// the recap slot on every later turn, in prose.
func TestStripPromptEchoKeepsOnlyTheRecord(t *testing.T) {
	echoed := "Vesper was expelled from the Guild. The fee is unresolved.\n\n" +
		markerNext + "\n" +
		"Christian: \"I am not asking as a courier.\"\n\n" +
		"Vesper Quill: *She tapped her pen against the edge of the map.*"
	got := stripPromptEcho(echoed)
	if strings.Contains(got, markerNext) {
		t.Errorf("the heading survived:\n%s", got)
	}
	if strings.Contains(got, "not asking as a courier") {
		t.Errorf("the transcript survived:\n%s", got)
	}
	if !strings.Contains(got, "expelled from the Guild") {
		t.Errorf("the record itself was lost:\n%s", got)
	}
}

func TestStripPromptEchoLeavesARealRecordAlone(t *testing.T) {
	clean := "Vesper was expelled from the Guild. Christian sat without being asked."
	if got := stripPromptEcho(clean); got != clean {
		t.Errorf("a clean record was cut:\n in:  %s\n out: %s", clean, got)
	}
}

// Every marker has to be caught, including the one used on the first pass when
// there is no previous record.
func TestStripPromptEchoCatchesEveryMarker(t *testing.T) {
	for _, marker := range []string{markerRecord, markerNext, markerFirst} {
		in := "A fact was established. " + marker + "\nsomething else"
		if got := stripPromptEcho(in); got != "A fact was established." {
			t.Errorf("marker %q not cut: %q", marker, got)
		}
	}
}

// And a reply that was nothing but the prompt read back has to come out empty,
// so Compact refuses it rather than storing it.
func TestStripPromptEchoOnNothingButAnEcho(t *testing.T) {
	if got := stripPromptEcho(markerFirst + "\nChristian: \"Hello.\""); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

// scriptedServer answers each request with the next reply in the list, and
// records the system prompt it was sent each time.
func scriptedServer(t *testing.T, replies []string, systems *[]string) *httptest.Server {
	t.Helper()
	n := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if msgs, ok := req["messages"].([]any); ok && len(msgs) > 0 {
			if first, ok := msgs[0].(map[string]any); ok {
				*systems = append(*systems, fmt.Sprint(first["content"]))
			}
		}
		reply := replies[min(n, len(replies)-1)]
		n++
		resp, _ := json.Marshal(map[string]any{
			"message": map[string]string{"role": "assistant", "content": reply},
			"done":    true, "done_reason": "stop", "eval_count": 50, "eval_duration": 1e9,
		})
		fmt.Fprintln(w, string(resp))
	}))
}

// agedTurns is a transcript of a known size, so a reply can be made
// deliberately too long or short against it.
func agedTurns(chars int) []ollama.Message {
	return []ollama.Message{{Role: ollama.RoleUser, Content: strings.Repeat("a", chars)}}
}

// bloatedRecord is a record that is too long without repeating itself, which is
// the case the deduplication cannot help with and the retry exists for. Every
// sentence is different, so nothing is dropped.
func bloatedRecord(sentences int) string {
	var b strings.Builder
	for i := 0; i < sentences; i++ {
		fmt.Fprintf(&b, "Fact number %d was established in the scene. ", i)
	}
	return strings.TrimSpace(b.String())
}

// A record nearly as long as the scene it replaces is about to be carried in
// every later prompt, so it is worth one more ask.
func TestCompactAsksAgainWhenTheRecordDoesNotCompact(t *testing.T) {
	long := bloatedRecord(30) // ~1250 chars, no two sentences alike
	short := "Vesper showed Wren the coastline."
	var systems []string
	srv := scriptedServer(t, []string{long, short}, &systems)
	defer srv.Close()

	out, err := Compact(context.Background(), ollama.NewClient(srv.URL), "m", "",
		agedTurns(2000), Character{Name: "Vesper"}, Persona{Name: "Wren"},
		ollama.Options{}, testBudget())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(systems) != 2 {
		t.Fatalf("made %d attempts, want 2", len(systems))
	}
	if out != short {
		t.Errorf("kept the long record: %q", out)
	}
	if !strings.Contains(systems[1], "Write a shorter one") {
		t.Errorf("the second attempt was not told why:\n%s", systems[1])
	}
	if strings.Contains(systems[0], "Write a shorter one") {
		t.Error("the first attempt was already being told off")
	}
}

// One ask only. A model that is no better the second time must not be asked a
// third, and the better of the two is what gets kept.
func TestCompactAsksOnlyOnceMoreAndKeepsTheBetter(t *testing.T) {
	first := bloatedRecord(30)  // ~1300 chars, no two sentences alike
	second := bloatedRecord(60) // ~2700, worse than the first
	var systems []string
	srv := scriptedServer(t, []string{first, second}, &systems)
	defer srv.Close()

	out, err := Compact(context.Background(), ollama.NewClient(srv.URL), "m", "",
		agedTurns(2000), Character{Name: "Vesper"}, Persona{Name: "Wren"},
		ollama.Options{}, testBudget())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(systems) != 2 {
		t.Errorf("made %d attempts, want exactly 2", len(systems))
	}
	if len(out) > len(first) {
		t.Errorf("kept the worse second answer (%d chars)", len(out))
	}
}

// A record that already compacts is left alone, and costs one call.
func TestCompactDoesNotAskAgainWhenTheRecordIsFine(t *testing.T) {
	var systems []string
	srv := scriptedServer(t, []string{"Vesper showed Wren the coastline."}, &systems)
	defer srv.Close()

	if _, err := Compact(context.Background(), ollama.NewClient(srv.URL), "m", "",
		agedTurns(2000), Character{Name: "Vesper"}, Persona{Name: "Wren"},
		ollama.Options{}, testBudget()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(systems) != 1 {
		t.Errorf("made %d calls for a perfectly good record, want 1", len(systems))
	}
}
