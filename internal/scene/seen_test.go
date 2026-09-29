package scene

import (
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/world"
)

// What the Model Sees lists the same lore and moments the turn sends, and
// says why each entry is there.
func TestWhatItSeesMatchesTheTurn(t *testing.T) {
	st := memoryStore(t)
	wid, err := st.SaveWorld(world.World{Name: "The Drowned Coast"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []world.Entry{
		{WorldID: wid, Name: "Kestrel Bay", Keys: []string{"Kestrel Bay"}, Content: "Ferries run late. The Guild runs the docks.", Enabled: true},
		{WorldID: wid, Name: "The Guild", Keys: []string{"Guild"}, Content: "Cartographers who expel their own.", Enabled: true},
		{WorldID: wid, Name: "Tides", Keys: []string{"tide"}, Content: "Nobody sails after dark.", Enabled: true, Constant: true},
	} {
		if _, err := st.SaveLoreEntry(e); err != nil {
			t.Fatal(err)
		}
	}
	cid, _ := st.SaveCharacter(chars.Character{Name: "Vesper", Description: "A cartographer.", WorldID: wid})
	ca, _ := st.Character(cid)
	ch, _ := st.NewChat(cid, "Scene", "m", store.KindRoleplay)
	add := func(role, text string) int64 {
		mid, err := st.AddMessage(store.Message{ChatID: ch.ID, Role: role, Content: text})
		if err != nil {
			t.Fatal(err)
		}
		return mid
	}
	pin := add(ollama.RoleAssistant, `*She pressed the brass key into your palm.* "Keep it safe."`)
	if err := st.SetMessagePinned(pin, true); err != nil {
		t.Fatal(err)
	}
	upto := add(ollama.RoleUser, `"Tell me about the maps," I say.`)
	if err := st.SetChatSummary(ch.ID, "Wren arrived late. Vesper talked about maps.", upto); err != nil {
		t.Fatal(err)
	}
	ch, _ = st.Chat(ch.ID)
	hist := []ollama.Message{{Role: ollama.RoleUser, Content: "We should take the ferry across Kestrel Bay."}}

	seen := WhatItSees(st, store.Config{}, ch, []chars.Character{ca}, hist)
	if seen.World != "The Drowned Coast" || seen.Record == 0 {
		t.Errorf("world %q, record %d", seen.World, seen.Record)
	}
	why := map[string]string{}
	for _, l := range seen.Lore {
		why[l.Name] = l.Why
	}
	for name, want := range map[string]string{
		"Tides":       "Always sent.",
		"Kestrel Bay": "“Kestrel Bay” was mentioned.",
		"The Guild":   "Named in Kestrel Bay.",
	} {
		if why[name] != want {
			t.Errorf("%s: %q, want %q", name, why[name], want)
		}
	}
	if len(seen.Pinned) != 1 || seen.Pinned[0].Who != "Vesper" {
		t.Errorf("pinned: %+v", seen.Pinned)
	}
	// And the turn itself sends what was listed.
	turn := BuildFor(st, store.Config{}, ch, []chars.Character{ca}, hist)
	var sent string
	for _, m := range turn {
		sent += m.Content
	}
	for _, want := range []string{"Ferries run late", "expel their own", "Nobody sails after dark", "brass key"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the turn does not send %q", want)
		}
	}
}
