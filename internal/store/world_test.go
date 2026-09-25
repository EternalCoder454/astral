package store

import (
	"path/filepath"
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/world"
)

func TestWorldRoundTrip(t *testing.T) {
	s := openTest(t)
	id, err := s.SaveWorld(world.World{Name: "The Drowned Coast", Description: "Maps go out of date."})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.World(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "The Drowned Coast" || got.Description != "Maps go out of date." {
		t.Errorf("round trip = %+v", got)
	}

	got.Name = "The Drowned Coast (revised)"
	if _, err := s.SaveWorld(got); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Worlds()
	if len(all) != 1 {
		t.Errorf("update created a second world: %d total", len(all))
	}
}

func TestSaveWorldNeedsAName(t *testing.T) {
	s := openTest(t)
	if _, err := s.SaveWorld(world.World{Name: "   "}); err == nil {
		t.Error("a nameless world was accepted")
	}
}

// Identity is (world, name): it is what the model has when it learns more
// about a subject, and it is what makes re-learning an update rather than a
// second entry for the same thing.
func TestLoreUpsertsByName(t *testing.T) {
	s := openTest(t)
	wid, _ := s.SaveWorld(world.World{Name: "Coast"})

	first, err := s.SaveLoreEntry(world.Entry{
		WorldID: wid, Name: "Kestrel Bay", Keys: []string{"Kestrel Bay"},
		Content: "A port city.", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveLoreEntry(world.Entry{
		WorldID: wid, Name: "Kestrel Bay", Keys: []string{"Kestrel Bay", "the Bay"},
		Content: "A port city three days north. Its ferries are unreliable.", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("re-learning the same subject made a new entry: %d then %d", first, second)
	}
	entries, _ := s.LoreEntries(wid)
	if len(entries) != 1 {
		t.Fatalf("world has %d entries, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Content, "three days north") {
		t.Errorf("content was not updated: %q", entries[0].Content)
	}
	if strings.Join(entries[0].Keys, ",") != "Kestrel Bay,the Bay" {
		t.Errorf("keys = %v", entries[0].Keys)
	}
}

// Someone who wrote lore by hand made a decision. A background process
// replacing it while they were reading a reply would be the worst kind of
// surprise, so it is refused.
func TestAutomaticUpdatesDoNotOverwriteHandWrittenLore(t *testing.T) {
	s := openTest(t)
	wid, _ := s.SaveWorld(world.World{Name: "Coast"})

	if _, err := s.SaveLoreEntry(world.Entry{
		WorldID: wid, Name: "Kestrel Bay", Content: "Mine, and correct.",
		Keys: []string{"Kestrel Bay"}, Enabled: true, Auto: false,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := s.SaveLoreEntry(world.Entry{
		WorldID: wid, Name: "Kestrel Bay", Content: "The model's guess.",
		Keys: []string{"Kestrel Bay"}, Enabled: true, Auto: true,
	})
	if err != ErrWouldOverwriteManual {
		t.Fatalf("err = %v, want ErrWouldOverwriteManual", err)
	}
	entries, _ := s.LoreEntries(wid)
	if entries[0].Content != "Mine, and correct." {
		t.Errorf("the hand-written entry was overwritten: %q", entries[0].Content)
	}

	// An automatic entry may be updated automatically, and a person may
	// overwrite anything.
	if _, err := s.SaveLoreEntry(world.Entry{
		WorldID: wid, Name: "Kestrel Bay", Content: "Corrected by hand.",
		Keys: []string{"Kestrel Bay"}, Enabled: true, Auto: false,
	}); err != nil {
		t.Errorf("a person could not overwrite their own entry: %v", err)
	}
}

func TestLowConfidenceEntriesArriveDisabled(t *testing.T) {
	s := openTest(t)
	wid, _ := s.SaveWorld(world.World{Name: "Coast"})
	if _, err := s.SaveLoreEntry(world.Entry{
		WorldID: wid, Name: "A rumour", Content: "Possibly true.",
		Keys: []string{"rumour"}, Enabled: false, Auto: true, Confidence: 0.4,
	}); err != nil {
		t.Fatal(err)
	}
	entries, _ := s.LoreEntries(wid)
	if entries[0].Enabled {
		t.Error("a low confidence entry was enabled")
	}
	if entries[0].Confidence != 0.4 {
		t.Errorf("confidence = %v, want 0.4", entries[0].Confidence)
	}
	// And it must not reach a scene while it is disabled.
	if hits := world.Match(entries, "they spoke of the rumour", world.BudgetChars); len(hits) != 0 {
		t.Error("a disabled entry was injected")
	}
}

// Deleting a world must not delete the characters who lived in it. The lore
// goes, because it only meant anything there.
func TestDeletingAWorldKeepsItsCharacters(t *testing.T) {
	s := openTest(t)
	wid, _ := s.SaveWorld(world.World{Name: "Coast"})
	s.SaveLoreEntry(world.Entry{WorldID: wid, Name: "Kestrel Bay", Content: "A port.",
		Keys: []string{"Kestrel Bay"}, Enabled: true})
	cid, err := s.SaveCharacter(chars.Character{Name: "Vesper", WorldID: wid})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteWorld(wid); err != nil {
		t.Fatal(err)
	}
	got, err := s.Character(cid)
	if err != nil {
		t.Fatalf("the character was deleted with the world: %v", err)
	}
	if got.WorldID != 0 {
		t.Errorf("WorldID = %d, want 0 after the world was deleted", got.WorldID)
	}
	if n, _ := s.CountLore(wid); n != 0 {
		t.Errorf("%d lore entries outlived their world", n)
	}
}

func TestLoreEntryNeedsNameAndWorld(t *testing.T) {
	s := openTest(t)
	wid, _ := s.SaveWorld(world.World{Name: "Coast"})
	if _, err := s.SaveLoreEntry(world.Entry{WorldID: wid, Name: "  ", Content: "x"}); err == nil {
		t.Error("a nameless entry was accepted")
	}
	if _, err := s.SaveLoreEntry(world.Entry{Name: "Orphan", Content: "x"}); err == nil {
		t.Error("an entry with no world was accepted")
	}
}

// A database written before worlds existed must gain the tables and the
// characters.world_id column without disturbing what is in it.
func TestMigrationAddsWorlds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	path := filepath.Join(dir, "astral.db")

	s, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cid, err := s.SaveCharacter(chars.Character{Name: "Vesper", Description: "A cartographer."})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopening runs the migrations again, which must be a no-op.
	s2, recovered, err := Open(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer s2.Close()
	if recovered {
		t.Fatal("reopening quarantined a healthy database")
	}
	got, err := s2.Character(cid)
	if err != nil || got.Description != "A cartographer." {
		t.Errorf("character disturbed by migration: %+v err=%v", got, err)
	}
	if got.WorldID != 0 {
		t.Errorf("WorldID = %d, want 0 for a character predating worlds", got.WorldID)
	}
	if _, err := s2.SaveWorld(world.World{Name: "works"}); err != nil {
		t.Errorf("writing a world to a migrated database: %v", err)
	}
}
