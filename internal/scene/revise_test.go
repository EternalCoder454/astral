package scene

import (
	"path/filepath"
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/store"
	"astral/internal/world"
)

// A designer chat that names something is revising it. All three work the same
// way and all three carry it on a column the chat already had, which is the part
// worth a test: the wiring is a condition in a switch, and a condition that never
// fires leaves you interviewing about a blank page.
func TestDesignerChatsRevise(t *testing.T) {
	st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	charID, err := st.SaveCharacter(chars.Character{
		Name: "Vesper", Description: "A cartographer who does not look up.",
	})
	if err != nil {
		t.Fatal(err)
	}
	worldID, err := st.SaveWorld(world.World{
		Name: "Sever Reach", Rules: "Nobody sails after dark.",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Config{
		PersonaName:   "Wren",
		WritingStyles: []chars.WritingStyle{{Name: "Sparse", Instructions: "Length: one paragraph."}},
	}

	t.Run("character", func(t *testing.T) {
		ca, _ := st.Character(charID)
		got := Build(st, cfg, store.Chat{ID: 1, Kind: store.KindDesigner, CharacterID: charID}, ca, nil)
		sys := got[0].Content
		if !strings.Contains(sys, "A cartographer who does not look up.") {
			t.Error("the designer was not shown the card it is revising")
		}
		if !strings.Contains(sys, "Save Character") {
			t.Error("the revision prompt was not used")
		}
	})

	t.Run("world", func(t *testing.T) {
		got := Build(st, cfg, store.Chat{ID: 2, Kind: store.KindWorldDesigner, WorldID: worldID}, chars.Character{}, nil)
		sys := got[0].Content
		if !strings.Contains(sys, "Nobody sails after dark.") {
			t.Error("the designer was not shown the world it is revising")
		}
		if !strings.Contains(sys, "Save World") {
			t.Error("the revision prompt was not used")
		}
		if !strings.Contains(sys, "lorebook is not being rewritten") && !strings.Contains(sys, "lorebook is empty") {
			t.Error("nothing tells it to leave the lorebook alone")
		}
	})

	t.Run("style", func(t *testing.T) {
		got := Build(st, cfg, store.Chat{ID: 3, Kind: store.KindStyleDesigner, Note: "Sparse"}, chars.Character{}, nil)
		sys := got[0].Content
		if !strings.Contains(sys, "Length: one paragraph.") {
			t.Error("the designer was not shown the style it is revising")
		}
		if !strings.Contains(sys, "Save Style") {
			t.Error("the revision prompt was not used")
		}
	})

	// And with nothing named, each is inventing rather than revising.
	t.Run("fresh", func(t *testing.T) {
		for _, kind := range []string{store.KindDesigner, store.KindWorldDesigner, store.KindStyleDesigner} {
			sys := Build(st, cfg, store.Chat{ID: 4, Kind: kind}, chars.Character{}, nil)[0].Content
			if strings.Contains(sys, "as it stands") {
				t.Errorf("%s opened as a revision with nothing to revise", kind)
			}
		}
	})

	// A style whose name no longer exists falls back to inventing one rather
	// than interviewing about a style it cannot show.
	t.Run("style that was deleted", func(t *testing.T) {
		sys := Build(st, cfg, store.Chat{ID: 5, Kind: store.KindStyleDesigner, Note: "Gone"}, chars.Character{}, nil)[0].Content
		if strings.Contains(sys, "as it stands") {
			t.Error("a deleted style was still treated as a revision")
		}
	})
}
