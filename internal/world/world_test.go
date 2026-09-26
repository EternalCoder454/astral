package world

import (
	"fmt"
	"strings"
	"testing"
)

func entry(id int64, name, content string, keys ...string) Entry {
	return Entry{ID: id, Name: name, Content: content, Keys: keys, Enabled: true}
}

func names(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func TestMatchOnlySendsWhatWasMentioned(t *testing.T) {
	entries := []Entry{
		entry(1, "Kestrel Bay", "A port city three days north.", "Kestrel Bay", "the Bay"),
		entry(2, "The Ledger", "A book of debts nobody admits to owing.", "Ledger"),
		entry(3, "Vesper", "A cartographer.", "Vesper"),
	}
	got := names(Match(entries, `"The ferry from Kestrel Bay was held."`, BudgetChars))
	if strings.Join(got, ",") != "Kestrel Bay" {
		t.Errorf("matched %v, want only Kestrel Bay", got)
	}
}

// The failure a naive substring search produces, and the reason for the word
// boundary check: a short key would otherwise fire constantly and spend the
// budget that real matches needed.
func TestMatchRespectsWordBoundaries(t *testing.T) {
	entries := []Entry{entry(1, "Ash", "A courier who never removes her gloves.", "Ash")}

	for _, miss := range []string{
		"He had no cash on him.",
		"She was ashamed of it.",
		"A flash of movement.",
		"Washington.",
	} {
		if hits := Match(entries, miss, BudgetChars); len(hits) != 0 {
			t.Errorf("%q matched Ash, it should not", miss)
		}
	}
	for _, hit := range []string{
		"Ash arrived late.",
		"He nodded at Ash.",
		`"Ash?" she said.`,
		"ash, of all people",
	} {
		if hits := Match(entries, hit, BudgetChars); len(hits) != 1 {
			t.Errorf("%q did not match Ash, it should", hit)
		}
	}
}

func TestMatchIsCaseInsensitive(t *testing.T) {
	entries := []Entry{entry(1, "Kestrel Bay", "A port city.", "Kestrel Bay")}
	for _, text := range []string{"kestrel bay", "KESTREL BAY", "Kestrel Bay"} {
		if len(Match(entries, text, BudgetChars)) != 1 {
			t.Errorf("%q did not match", text)
		}
	}
}

func TestConstantEntriesAlwaysGoOut(t *testing.T) {
	entries := []Entry{
		{ID: 1, Name: "The Tide", Content: "It comes in early now.", Enabled: true, Constant: true},
		entry(2, "Vesper", "A cartographer.", "Vesper"),
	}
	got := names(Match(entries, "nothing relevant was said", BudgetChars))
	if strings.Join(got, ",") != "The Tide" {
		t.Errorf("matched %v, want the constant entry only", got)
	}
}

func TestDisabledAndEmptyEntriesAreSkipped(t *testing.T) {
	entries := []Entry{
		{ID: 1, Name: "Off", Content: "text", Keys: []string{"Vesper"}, Enabled: false},
		{ID: 2, Name: "Blank", Content: "   ", Keys: []string{"Vesper"}, Enabled: true},
		entry(3, "Live", "text", "Vesper"),
	}
	got := names(Match(entries, "Vesper looked up.", BudgetChars))
	if strings.Join(got, ",") != "Live" {
		t.Errorf("matched %v, want only the live entry", got)
	}
}

// Lore competes with the transcript for the same context window. When more is
// triggered than fits, the least important has to be what goes.
func TestBudgetDropsLowestPriorityFirst(t *testing.T) {
	big := strings.Repeat("x", 400)
	entries := []Entry{
		{ID: 1, Name: "low", Content: big, Keys: []string{"town"}, Enabled: true, Priority: 1},
		{ID: 2, Name: "high", Content: big, Keys: []string{"town"}, Enabled: true, Priority: 10},
		{ID: 3, Name: "mid", Content: big, Keys: []string{"town"}, Enabled: true, Priority: 5},
	}
	got := names(Match(entries, "back in town", 900))
	if strings.Join(got, ",") != "high,mid" {
		t.Errorf("kept %v, want the two highest priorities", got)
	}
}

// A single oversized entry must not block everything behind it.
func TestBudgetSkipsAnOversizedEntryButKeepsGoing(t *testing.T) {
	entries := []Entry{
		{ID: 1, Name: "huge", Content: strings.Repeat("x", 5000), Keys: []string{"town"}, Enabled: true, Priority: 10},
		{ID: 2, Name: "small", Content: "short and useful", Keys: []string{"town"}, Enabled: true, Priority: 1},
	}
	got := names(Match(entries, "back in town", 500))
	if strings.Join(got, ",") != "small" {
		t.Errorf("kept %v, want the entry that fits", got)
	}
}

// Something mentioned an hour ago should stop being injected, or a long scene
// ends up sending its entire lorebook every turn.
func TestScanDepthForgetsOldMentions(t *testing.T) {
	entries := []Entry{entry(1, "Kestrel Bay", "A port city.", "Kestrel Bay")}
	old := "They spoke of Kestrel Bay. " + strings.Repeat("Later, nothing relevant happened. ", 400)
	if len(old) <= ScanDepthChars {
		t.Fatalf("test text is only %d chars, shorter than the scan depth", len(old))
	}
	if hits := Match(entries, old, BudgetChars); len(hits) != 0 {
		t.Error("a mention far outside the scan depth still matched")
	}
}

func TestMatchOrderIsStable(t *testing.T) {
	entries := []Entry{
		entry(3, "c", "x", "town"), entry(1, "a", "x", "town"), entry(2, "b", "x", "town"),
	}
	first := strings.Join(names(Match(entries, "town", BudgetChars)), ",")
	for i := 0; i < 20; i++ {
		if got := strings.Join(names(Match(entries, "town", BudgetChars)), ","); got != first {
			t.Fatalf("order changed between runs: %q then %q", first, got)
		}
	}
	if first != "a,b,c" {
		t.Errorf("order = %q, want oldest first at equal priority", first)
	}
}

func TestRenderWritesFactsNotProse(t *testing.T) {
	w := World{Name: "The Drowned Coast", Description: "Maps here go out of date."}
	out := Render(w, []Entry{
		entry(1, "Kestrel Bay", "A port city\n   three days north.", "Kestrel Bay"),
	})
	for _, want := range []string{"The Drowned Coast", "Maps here go out of date.",
		"established fact", "Kestrel Bay: A port city three days north."} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\n   ") {
		t.Errorf("content was not collapsed to one line:\n%s", out)
	}
}

// A world you have written and not yet filled with lore is still a setting.
// Returning nothing until an entry happened to match meant a world created five
// minutes ago reached the model as silence, which is not what anyone would
// expect of the thing they had just written.
func TestRenderSendsTheSettingWithNoEntries(t *testing.T) {
	w := World{Name: "Kestrel Bay", Description: "A harbour town under permanent rain.",
		Rules: "Nobody sails east of the Sever. The Guild licenses every chart."}
	got := Render(w, nil)
	for _, want := range []string{"Kestrel Bay", "permanent rain", "east of the Sever", "Guild licenses"} {
		if !strings.Contains(got, want) {
			t.Errorf("render is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "What is true here") {
		t.Errorf("an empty lorebook produced an empty list heading:\n%s", got)
	}
}

// A world with nothing written in it and nothing matched is nothing to say.
func TestRenderIsEmptyForAnEmptyWorld(t *testing.T) {
	if got := Render(World{}, nil); got != "" {
		t.Errorf("render of an empty world = %q, want empty", got)
	}
}

// The rules are sent every turn out of the budget the entries share, so they
// are bounded and cut at the end of a rule rather than mid-sentence.
func TestRulesAreBounded(t *testing.T) {
	long := strings.Repeat("Nobody sails east of the Sever. ", 60)
	got := Render(World{Name: "Kestrel Bay", Rules: long}, nil)
	if len(got) > rulesChars+200 {
		t.Errorf("render is %d chars; the rules were not bounded", len(got))
	}
	if !strings.HasSuffix(strings.TrimSpace(got), ".") {
		t.Errorf("the rules were cut mid-sentence:\n%s", got[max(0, len(got)-60):])
	}
}

func TestRecentTextTakesTheTail(t *testing.T) {
	var turns []string
	for i := 0; i < 200; i++ {
		turns = append(turns, fmt.Sprintf("turn %d ", i)+strings.Repeat("x", 100))
	}
	got := RecentText(turns)
	if len(got) > ScanDepthChars+200 {
		t.Errorf("took %d chars, well over the %d depth", len(got), ScanDepthChars)
	}
	if !strings.Contains(got, "turn 199") {
		t.Error("the newest turn was not included")
	}
	if strings.Contains(got, "turn 0 ") {
		t.Error("the oldest turn was included")
	}
}
