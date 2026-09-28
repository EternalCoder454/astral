package store

import (
	"strings"
	"testing"
)

func TestKnowledgeIsFoundByWhatItIsAbout(t *testing.T) {
	s := openTest(t)
	tides, err := s.SaveKnowledge(KnowledgeEntry{
		Title: "Kestrel Bay tides",
		Body:  "High water comes forty minutes later each day. The spring tides flood the lower market.",
		Tags:  []string{"Harbour", "harbour", " tides "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveKnowledge(KnowledgeEntry{Title: "Brass dividers", Body: "A drafting tool with two points."}); err != nil {
		t.Fatal(err)
	}

	// A question, not keywords, and a plural the entry never uses: the stemmer
	// is what makes "tide" and "tides" the same word.
	hits, err := s.SearchKnowledgeText("When does the tide flood the market?", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].EntryID != tides {
		t.Fatalf("want the tides entry first, got %+v", hits)
	}
	e, err := s.Knowledge(tides)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.Tags, ",") != "harbour,tides" {
		t.Errorf("tags were not cleaned: %v", e.Tags)
	}
}

func TestTheTitleOutranksAPassingMention(t *testing.T) {
	s := openTest(t)
	passing, _ := s.SaveKnowledge(KnowledgeEntry{Title: "Market days", Body: "Fish, rope, and on Thursdays the ferry timetable is posted."})
	about, _ := s.SaveKnowledge(KnowledgeEntry{Title: "Ferry timetable", Body: "Departures at nine and at four."})
	hits, _ := s.SearchKnowledgeText("ferry timetable", 5)
	if len(hits) < 2 || hits[0].EntryID != about || hits[1].EntryID != passing {
		t.Errorf("the entry named for it should come first: %+v", hits)
	}
}

func TestEditingReindexesAndDeletingLeavesNothing(t *testing.T) {
	s := openTest(t)
	id, _ := s.SaveKnowledge(KnowledgeEntry{Title: "Lighthouse", Body: "The keeper is called Marius."})
	if _, err := s.SaveKnowledge(KnowledgeEntry{ID: id, Title: "Lighthouse", Body: "The keeper is called Odile."}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.SearchKnowledgeText("Marius", 5); len(hits) != 0 {
		t.Errorf("the old text is still indexed: %+v", hits)
	}
	if hits, _ := s.SearchKnowledgeText("Odile", 5); len(hits) != 1 {
		t.Errorf("the new text is not indexed: %+v", hits)
	}
	if err := s.DeleteKnowledge(id); err != nil {
		t.Fatal(err)
	}
	var fts, chunks int
	s.db.QueryRow(`SELECT COUNT(*) FROM knowledge_fts`).Scan(&fts)
	s.db.QueryRow(`SELECT COUNT(*) FROM knowledge_chunks`).Scan(&chunks)
	if fts != 0 || chunks != 0 {
		t.Errorf("deleting left %d index rows and %d chunks", fts, chunks)
	}
}

func TestAPageIsKeptOnce(t *testing.T) {
	s := openTest(t)
	first, _ := s.SaveKnowledgeBySource(KnowledgeEntry{Title: "Notes", Body: "Old.", Source: "https://example.org/a", Origin: OriginWeb})
	second, _ := s.SaveKnowledgeBySource(KnowledgeEntry{Title: "Notes", Body: "New.", Source: "https://example.org/a", Origin: OriginWeb})
	if first != second || s.KnowledgeCount() != 1 {
		t.Errorf("the same page was saved twice: %d, %d, count %d", first, second, s.KnowledgeCount())
	}
	e, _ := s.Knowledge(first)
	if e.Body != "New." {
		t.Errorf("the page was not updated: %q", e.Body)
	}
}

func TestQueryTextCannotBecomeSyntax(t *testing.T) {
	s := openTest(t)
	s.SaveKnowledge(KnowledgeEntry{Title: "Tide", Body: "Water."})
	// Operators, quotes, column filters and a bare asterisk: all words to
	// search for, and none of them an error.
	for _, q := range []string{`tide NOT water`, `"tide`, `title:tide`, `tide*`, `(tide) AND -water`, `***`, ``} {
		if _, err := s.SearchKnowledgeText(q, 5); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
	if q := FTSQuery(`What is the "tide" timetable?`); q != `"tide" OR "timetable"` {
		t.Errorf("query was %s", q)
	}
}

func TestVectorsAreComparedOnlyWithinOneModel(t *testing.T) {
	s := openTest(t)
	a, _ := s.SaveKnowledge(KnowledgeEntry{Title: "A", Body: "alpha"})
	b, _ := s.SaveKnowledge(KnowledgeEntry{Title: "B", Body: "beta"})
	pending, _ := s.ChunksWithoutVector("m", 10)
	if len(pending) != 2 {
		t.Fatalf("want 2 chunks to embed, got %d", len(pending))
	}
	ids := map[int64]int64{}
	for _, p := range pending {
		var entry int64
		s.db.QueryRow(`SELECT entry_id FROM knowledge_chunks WHERE id = ?`, p.ID).Scan(&entry)
		ids[entry] = p.ID
	}
	s.SetChunkVector(ids[a], "m", []float32{1, 0, 0})
	s.SetChunkVector(ids[b], "m", []float32{0, 1, 0})

	hits, err := s.SearchKnowledgeVector("m", []float32{0.9, 0.1, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].EntryID != a {
		t.Errorf("nearest first: %+v", hits)
	}
	if hits, _ := s.SearchKnowledgeVector("other", []float32{1, 0, 0}, 5); len(hits) != 0 {
		t.Errorf("vectors from another model were compared: %+v", hits)
	}
	if left, _ := s.ChunksWithoutVector("m", 10); len(left) != 0 {
		t.Errorf("%d chunks still waiting", len(left))
	}
	// A different model has everything still to do.
	if left, _ := s.ChunksWithoutVector("other", 10); len(left) != 2 {
		t.Errorf("switching models left %d to embed, want 2", len(left))
	}
}

func TestChunkTextKeepsParagraphsAndBoundsLength(t *testing.T) {
	long := strings.Repeat("The tide came in early. ", 80)
	chunks := ChunkText("Short one.\n\nShort two.\n\n" + long)
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks", len(chunks))
	}
	if !strings.HasPrefix(chunks[0], "Short one.\n\nShort two.") {
		t.Errorf("short paragraphs were not packed together: %q", chunks[0])
	}
	for i, c := range chunks {
		if len(c) > ChunkChars {
			t.Errorf("chunk %d is %d characters", i, len(c))
		}
		if i > 0 && !strings.HasSuffix(strings.TrimSpace(c), ".") && i < len(chunks)-1 {
			t.Errorf("chunk %d was cut mid-sentence: %q", i, c[len(c)-20:])
		}
	}
	if ChunkText("  \n\n ") != nil {
		t.Error("an empty body produced chunks")
	}
}

func TestAnEntryNeedsSomething(t *testing.T) {
	s := openTest(t)
	if _, err := s.SaveKnowledge(KnowledgeEntry{}); err == nil {
		t.Error("an empty entry was saved")
	}
	id, err := s.SaveKnowledge(KnowledgeEntry{Body: "Untitled thought\nwith a second line"})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := s.Knowledge(id)
	if e.Title != "Untitled thought" {
		t.Errorf("title taken from the body: %q", e.Title)
	}
}
