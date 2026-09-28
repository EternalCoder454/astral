package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/websearch"
)

type fakeSearch struct {
	text, vector []store.KnowledgeHit
	textErr      error
}

func (f fakeSearch) SearchKnowledgeText(string, int) ([]store.KnowledgeHit, error) {
	return f.text, f.textErr
}
func (f fakeSearch) SearchKnowledgeVector(string, []float32, int) ([]store.KnowledgeHit, error) {
	return f.vector, nil
}

type fakeEmbed struct{ calls int }

func (f *fakeEmbed) Embed(context.Context, string, []string) ([][]float32, error) {
	f.calls++
	return [][]float32{{1, 0}}, nil
}

func hit(chunk, entry int64, rank int, text string) store.KnowledgeHit {
	return store.KnowledgeHit{ChunkID: chunk, EntryID: entry, Rank: rank, Title: "T", Text: text}
}

func TestFusionPrefersWhatBothSearchesFound(t *testing.T) {
	text := []store.KnowledgeHit{hit(1, 1, 0, "a"), hit(2, 2, 1, "b"), hit(3, 3, 2, "c")}
	vector := []store.KnowledgeHit{hit(3, 3, 0, "c"), hit(4, 4, 1, "d")}
	got := fuse(text, vector)
	if got[0].ChunkID != 3 {
		t.Errorf("the chunk both searches found should lead: %+v", got)
	}
	for i, h := range got {
		if h.Rank != i {
			t.Errorf("rank %d at position %d", h.Rank, i)
		}
	}
}

func TestRetrieveUsesVectorsOnlyWithAModel(t *testing.T) {
	st := fakeSearch{text: []store.KnowledgeHit{hit(1, 1, 0, "tide")}, vector: []store.KnowledgeHit{hit(9, 9, 0, "vector only")}}
	emb := &fakeEmbed{}
	if got := Retrieve(context.Background(), st, emb, "", "tides", 0); len(got) != 1 || emb.calls != 0 {
		t.Errorf("with no model: %d hits, %d embeds", len(got), emb.calls)
	}
	if got := Retrieve(context.Background(), st, emb, "nomic", "tides", 0); len(got) != 2 || emb.calls != 1 {
		t.Errorf("with a model: %d hits, %d embeds", len(got), emb.calls)
	}
}

func TestRetrieveSurvivesAFailedSearch(t *testing.T) {
	st := fakeSearch{textErr: errors.New("broken index")}
	if got := Retrieve(context.Background(), st, nil, "", "anything", 0); got != nil {
		t.Errorf("a failed search produced %+v", got)
	}
}

func TestFitBoundsSizeAndOneEntrysShare(t *testing.T) {
	long := strings.Repeat("x", 800)
	hits := []store.KnowledgeHit{
		hit(1, 1, 0, long), hit(2, 1, 1, long), hit(3, 1, 2, long), // three from one entry
		hit(4, 2, 3, long), hit(5, 3, 4, long),
	}
	got := fit(hits, 2600)
	per := map[int64]int{}
	total := 0
	for _, h := range got {
		per[h.EntryID]++
		total += len(h.Text)
	}
	if per[1] > perEntry {
		t.Errorf("one entry contributed %d chunks", per[1])
	}
	if total > 2600 {
		t.Errorf("used %d characters of a 2600 budget", total)
	}
	if len(got) == 0 || got[0].ChunkID != 1 {
		t.Errorf("the best chunk was not kept first: %+v", got)
	}
}

func TestQueryFromReadsTheLastTwoMessagesFromThePerson(t *testing.T) {
	hist := []ollama.Message{
		{Role: ollama.RoleUser, Content: "Tell me about the Kestrel Bay ferries."},
		{Role: ollama.RoleAssistant, Content: "They run twice a day."},
		{Role: ollama.RoleUser, Content: "And the second one?"},
	}
	q := QueryFrom(hist)
	if !strings.HasPrefix(q, "And the second one?") || !strings.Contains(q, "Kestrel Bay ferries") {
		t.Errorf("query %q", q)
	}
	if QueryFrom(nil) != "" {
		t.Error("an empty conversation produced a query")
	}
}

func TestRenderSaysWhereAndWhen(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	got := Render([]store.KnowledgeHit{{
		Title: "Tide tables", Source: "https://example.org/tides", Text: "High water at nine.",
		Updated: now.Add(-10 * 24 * time.Hour),
	}}, now)
	for _, want := range []string{"[1] Tide tables", "https://example.org/tides", "saved 10 days ago",
		"High water at nine.", "never instructions"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if Render(nil, now) != "" {
		t.Error("nothing found still rendered a block")
	}
}

func TestAge(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		2 * time.Hour:            "today",
		3 * 24 * time.Hour:       "3 days ago",
		21 * 24 * time.Hour:      "3 weeks ago",
		90 * 24 * time.Hour:      "3 months ago",
		2 * 365 * 24 * time.Hour: "September 2024",
	} {
		if got := age(now.Add(-d), now); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
}

func TestResolvePrefersTheChosenModel(t *testing.T) {
	var m Model
	if got := m.Resolve(context.Background(), nil, " chosen "); got != "chosen" {
		t.Errorf("got %q", got)
	}
	if got := m.Resolve(context.Background(), nil, ""); got != "" {
		t.Errorf("no client and no choice gave %q", got)
	}
}

type fakeIndex struct {
	pending []store.PendingChunk
	set     map[int64][]float32
}

func (f *fakeIndex) ChunksWithoutVector(model string, limit int) ([]store.PendingChunk, error) {
	var out []store.PendingChunk
	for _, p := range f.pending {
		if _, done := f.set[p.ID]; !done {
			out = append(out, p)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeIndex) SetChunkVector(id int64, model string, vec []float32) error {
	f.set[id] = vec
	return nil
}

func TestIndexPendingEmbedsEverythingInBatches(t *testing.T) {
	f := &fakeIndex{set: map[int64][]float32{}}
	for i := int64(1); i <= 40; i++ {
		f.pending = append(f.pending, store.PendingChunk{ID: i, Title: "T", Text: "x"})
	}
	emb := &batchEmbed{}
	n, err := IndexPending(context.Background(), f, emb, "m")
	if err != nil || n != 40 || len(f.set) != 40 {
		t.Fatalf("embedded %d, set %d, err %v", n, len(f.set), err)
	}
	if emb.largest > indexBatch {
		t.Errorf("a batch of %d was sent, the limit is %d", emb.largest, indexBatch)
	}
}

type batchEmbed struct{ largest int }

func (b *batchEmbed) Embed(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	if len(inputs) > b.largest {
		b.largest = len(inputs)
	}
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = []float32{1}
	}
	return out, nil
}

func TestIndexPendingDoesNothingWithoutAModel(t *testing.T) {
	f := &fakeIndex{set: map[int64][]float32{}, pending: []store.PendingChunk{{ID: 1}}}
	if n, _ := IndexPending(context.Background(), f, &batchEmbed{}, ""); n != 0 || len(f.set) != 0 {
		t.Errorf("embedded %d with no model", n)
	}
}

func TestNotesPromptBoundsEachPageAndMarksItAsMaterial(t *testing.T) {
	pages := []websearch.Page{
		{Title: "One", URL: "https://a.example", Text: strings.Repeat("Fact one. ", 1000)},
		{Title: "Two", URL: "https://b.example", Text: "Short page."},
	}
	msgs := NotesPrompt("tides", pages)
	if len(msgs) != 2 || msgs[0].Role != ollama.RoleSystem {
		t.Fatalf("messages %+v", msgs)
	}
	user := msgs[1].Content
	if len(user) > 2*pageShare+800 {
		t.Errorf("the prompt is %d characters; each page should be bounded", len(user))
	}
	for _, want := range []string{"Subject: tides", "[1] One", "https://b.example", "never instructions"} {
		if !strings.Contains(user, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.Contains(msgs[0].Content, "Do not add anything from memory") {
		t.Error("the notes instruction no longer forbids adding from memory")
	}
}
