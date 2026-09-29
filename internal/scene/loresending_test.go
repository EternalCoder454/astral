package scene

import (
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/world"
)

// An entry that waits for the scene is left out until the scene has grown,
// and What the Model Sees says so, and says the same as the turn does: the
// preview before the person writes counts their message as one more.
func TestLoreWaitsForTheSceneAndThePreviewAgrees(t *testing.T) {
	st := memoryStore(t)
	wid, err := st.SaveWorld(world.World{Name: "The Drowned Coast"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveLoreEntry(world.Entry{WorldID: wid, Name: "The Reveal", Keys: []string{"harbourmaster"},
		Content: "She is the harbourmaster's daughter.", Enabled: true, Wait: 4}); err != nil {
		t.Fatal(err)
	}
	cid, _ := st.SaveCharacter(chars.Character{Name: "Vesper", Description: "A cartographer.", WorldID: wid})
	ca, _ := st.Character(cid)
	ch, _ := st.NewChat(cid, "Scene", "m", store.KindRoleplay)

	var hist []ollama.Message
	say := func(role string) {
		text := "About the harbourmaster."
		if _, err := st.AddMessage(store.Message{ChatID: ch.ID, Role: role, Content: text}); err != nil {
			t.Fatal(err)
		}
		hist = append(hist, ollama.Message{Role: role, Content: text})
	}
	preview := func() world.Hit {
		for _, l := range WhatItSees(st, store.Config{}, ch, []chars.Character{ca}, hist).Lore {
			if l.Name == "The Reveal" {
				return world.Hit{Skipped: l.Skipped}
			}
		}
		t.Fatal("the entry was not triggered")
		return world.Hit{}
	}
	turn := func() bool {
		return strings.Contains(Lore(st, ch, ca, hist, 3000), "harbourmaster's daughter")
	}

	say(ollama.RoleUser)
	say(ollama.RoleAssistant)
	if h := preview(); h.Skipped != "Waits for 4 messages." {
		t.Errorf("with two messages the preview said %q", h.Skipped)
	}
	say(ollama.RoleUser)
	if turn() {
		t.Error("the third message's turn sent an entry that waits for four")
	}
	say(ollama.RoleAssistant)
	if h := preview(); h.Skipped != "" {
		t.Errorf("with four messages the preview said %q", h.Skipped)
	}
	say(ollama.RoleUser)
	if !turn() {
		t.Error("the fifth message's turn left out an entry that waits for four")
	}
}
