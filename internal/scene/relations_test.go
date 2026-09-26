package scene

import (
	"path/filepath"
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/store"
)

// The store knows relations by id and the prompt wants names, and the three lines
// that carry them across are exactly where a bug would sit unnoticed: a scene
// would simply read as though nobody had met.
func TestRelationsReachAGroupScene(t *testing.T) {
	st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var cast []chars.Character
	for _, name := range []string{"Vesper", "Kestrel", "Ash"} {
		id, err := st.SaveCharacter(chars.Character{Name: name, Description: name + " is here."})
		if err != nil {
			t.Fatal(err)
		}
		cast = append(cast, chars.Character{ID: id, Name: name, Description: name + " is here."})
	}
	if err := st.SetRelation(cast[0].ID, cast[1].ID, "She trained her, and neither mentions it."); err != nil {
		t.Fatal(err)
	}
	// A relation with somebody outside the scene, which must not be sent.
	if err := st.SetRelation(cast[0].ID, cast[2].ID, "They have never met."); err != nil {
		t.Fatal(err)
	}

	cfg := store.Config{PersonaName: "Wren", NumCtx: 8192}
	ch := store.Chat{ID: 1, Kind: store.KindRoleplay}

	msgs := BuildFor(st, cfg, ch, cast[:2], nil)
	sys := msgs[0].Content
	if !strings.Contains(sys, "She trained her, and neither mentions it.") {
		t.Errorf("the scene was not told how its two characters know each other:\n%s", sys)
	}
	if strings.Contains(sys, "They have never met.") {
		t.Error("a relation with somebody who is not in the scene was sent")
	}

	// And a two-hander has no pairs, so it is not told about anybody.
	solo := BuildFor(st, cfg, ch, cast[:1], nil)
	if strings.Contains(solo[0].Content, "trained her") {
		t.Error("a scene with one character was given a relation")
	}
}
