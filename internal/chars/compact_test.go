package chars

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestNeedsCompaction(t *testing.T) {
	if NeedsCompaction(turns(4, 100)) {
		t.Error("a short scene was marked for compaction")
	}
	if !NeedsCompaction(turns(40, 1000)) {
		t.Error("a long scene was not marked for compaction")
	}
}

func TestSplitForCompactionKeepsTheRecentHalf(t *testing.T) {
	history := turns(60, 1000)
	aged, recent := SplitForCompaction(history)

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
	if kept > KeepVerbatimChars+1000 {
		t.Errorf("kept %d chars verbatim, well over the %d budget", kept, KeepVerbatimChars)
	}
}

func TestSplitLeavesShortScenesAlone(t *testing.T) {
	history := turns(4, 100)
	aged, recent := SplitForCompaction(history)
	if len(aged) != 0 || len(recent) != len(history) {
		t.Errorf("a short scene was split: %d aged, %d recent", len(aged), len(recent))
	}
}

// A single turn larger than the whole verbatim budget must not produce an
// empty recent half — the model would have nothing to answer.
func TestSplitSurvivesOneEnormousTurn(t *testing.T) {
	history := []ollama.Message{
		{Role: ollama.RoleUser, Content: strings.Repeat("a", 5000)},
		{Role: ollama.RoleAssistant, Content: strings.Repeat("b", CompactThresholdChars+5000)},
	}
	aged, recent := SplitForCompaction(history)
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
		Character{Name: "Vesper"}, Persona{Name: "Wren"}, ollama.Options{})
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
		"previous", nil, Character{Name: "V"}, Persona{Name: "Z"}, ollama.Options{})
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
		Character{Name: "V"}, Persona{Name: "Z"}, ollama.Options{})
	if err == nil {
		t.Error("a server error was not reported")
	}
	if out != "previous" {
		t.Errorf("out = %q, want the previous record preserved", out)
	}
}

func TestRecapIsBounded(t *testing.T) {
	long := strings.Repeat("Vesper did something notable. ", 500)
	srv := compactServer(t, long, nil)
	defer srv.Close()

	out, err := Compact(context.Background(), ollama.NewClient(srv.URL), "m", "",
		[]ollama.Message{{Role: ollama.RoleUser, Content: "hi"}},
		Character{Name: "V"}, Persona{Name: "Z"}, ollama.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > recapBudgetChars {
		t.Errorf("recap is %d chars, over the %d budget", len(out), recapBudgetChars)
	}
	// Cut at a sentence end, not mid-fact.
	if !strings.HasSuffix(strings.TrimSpace(out), ".") {
		t.Errorf("recap was cut mid-sentence: %q", out[len(out)-40:])
	}
}

// The recap has to reach the model, before the transcript and marked as fact.
func TestRecapAppearsBeforeTheTranscript(t *testing.T) {
	msgs := BuildMessages(testChar(), Persona{Name: "Wren"},
		"Vesper admitted she has never left the city.",
		[]ollama.Message{{Role: ollama.RoleUser, Content: "Tell me more."}})

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
