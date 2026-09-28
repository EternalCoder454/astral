package scene

import (
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

func TestAPinnedMomentIsSentHoweverOld(t *testing.T) {
	st := memoryStore(t)
	id, _ := st.SaveCharacter(chars.Character{Name: "Vesper", Description: "A cartographer."})
	ch, _ := st.NewChat(id, "Scene", "m", store.KindRoleplay)
	pin, _ := st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant,
		Content: `*She wrote the number on your wrist in ink.* "Forty one. If anyone asks, that is the room you were in."`})
	upto, _ := st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: "I leave for the night."})
	st.SetMessagePinned(pin, true)
	st.SetChatSummary(ch.ID, "Wren stayed at the inn.", upto)
	ch, _ = st.Chat(ch.ID)
	cfg := store.DefaultConfig()
	ca, _ := st.Character(id)
	// Nothing in the new turns touches the pinned one: it is sent anyway.
	msgs := Build(st, cfg, ch, ca, []ollama.Message{{Role: ollama.RoleUser, Content: "Good morning."}})
	var block string
	for _, m := range msgs {
		if strings.HasPrefix(m.Content, chars.MemoryHeading) {
			block = m.Content
		}
	}
	if !strings.Contains(block, "Pinned, to be kept in mind always:") || !strings.Contains(block, "Forty one") {
		t.Fatalf("the pinned moment was not sent:\n%s", block)
	}
}

func TestSteeringATurn(t *testing.T) {
	cfg := store.DefaultConfig()
	cfg.PersonaName = "Wren"
	ch := store.Chat{ID: 0, Kind: store.KindRoleplay}
	one := []chars.Character{{Name: "Vesper", Description: "A cartographer."}}
	hist := []ollama.Message{{Role: ollama.RoleUser, Content: "Hello."}}

	plain := BuildFor(nil, cfg, ch, one, hist)
	noted := BuildTurn(nil, cfg, ch, one, hist, Turn{Note: "make it shorter"})
	if len(plain) != len(noted) {
		t.Fatalf("a note changed the shape of the request: %d vs %d messages", len(plain), len(noted))
	}
	last := noted[len(noted)-1].Content
	if !strings.Contains(last, "THIS REPLY.") || !strings.HasSuffix(last, "make it shorter]") {
		t.Fatalf("the note is not the end of the closing block:\n%s", last)
	}
	for i := range plain[:len(plain)-1] {
		if plain[i].Content != noted[i].Content {
			t.Fatalf("message %d changed, which throws away the cached prefix", i)
		}
	}

	// A general chat has no closing block, so the note gets its own message.
	gen := BuildTurn(nil, cfg, store.Chat{Kind: store.KindAssistant}, nil, hist, Turn{Note: "in French"})
	if l := gen[len(gen)-1]; l.Role != ollama.RoleSystem || !strings.Contains(l.Content, "in French") {
		t.Fatalf("a general chat's note went missing: %+v", l)
	}

	cast := []chars.Character{{ID: 1, Name: "Vesper"}, {ID: 2, Name: "Ilse"}, {ID: 3, Name: "Maro"}}
	chosen := BuildTurn(nil, cfg, ch, cast, hist, Turn{Speaker: "maro"})
	if a := chosen[len(chosen)-1].Content; !strings.Contains(a, "THIS TURN: Maro answers") {
		t.Fatalf("the chosen speaker was not named:\n%s", a)
	}
	// Carrying on without you: an old message naming Vesper does not decide it.
	talk := []ollama.Message{{Role: ollama.RoleUser, Content: "Vesper, sit."},
		{Role: ollama.RoleAssistant, Content: "Vesper: *She sits.*"}}
	onward := BuildTurn(nil, cfg, ch, cast, talk, Turn{Onward: true})
	a := onward[len(onward)-1].Content
	if !strings.Contains(a, "NOBODY IS WAITING ON Wren") || strings.Contains(a, "THIS TURN: Vesper") {
		t.Fatalf("carrying on:\n%s", a)
	}
}

func TestDraftSwapsTheClosingBlock(t *testing.T) {
	cfg := store.DefaultConfig()
	cfg.PersonaName = "Wren"
	ch := store.Chat{Kind: store.KindRoleplay}
	one := []chars.Character{{Name: "Vesper", Description: "A cartographer."}}
	hist := []ollama.Message{{Role: ollama.RoleAssistant, Content: `"Sit," she says.`}}
	reply := BuildFor(nil, cfg, ch, one, hist)
	draft := Draft(nil, cfg, ch, one, hist, "ask about the map")
	if len(draft) != len(reply) {
		t.Fatalf("draft has %d messages, a reply %d", len(draft), len(reply))
	}
	last := draft[len(draft)-1].Content
	if !strings.Contains(last, "you are not writing Vesper") || !strings.Contains(last, "ask about the map") ||
		strings.Contains(last, "{{") {
		t.Fatalf("draft closing block:\n%s", last)
	}
	if strings.Contains(last, "You are Vesper") {
		t.Fatal("the draft kept the reply's closing block")
	}
	if !CanDraft(ch, one) || CanDraft(store.Chat{Kind: store.KindAssistant}, nil) {
		t.Fatal("CanDraft")
	}
	if got := chars.CleanDraft("Wren: \"Fine.\"", "Wren"); got != `"Fine."` {
		t.Fatalf("CleanDraft left %q", got)
	}
	if got := chars.CleanDraft("Here is Wren's reply:\n*I nod.*", "Wren"); got != "*I nod.*" {
		t.Fatalf("CleanDraft left %q", got)
	}
}

func TestDraftStatesYourLength(t *testing.T) {
	cfg := store.DefaultConfig()
	cfg.PersonaName = "Wren"
	one := []chars.Character{{Name: "Vesper"}}
	hist := []ollama.Message{
		{Role: ollama.RoleUser, Content: `*I sit.* "One two three four."`},
		{Role: ollama.RoleAssistant, Content: "A long reply.\n\nIn two paragraphs."},
		{Role: ollama.RoleUser, Content: `*I stand up.* "Five six seven eight."`},
	}
	last := Draft(nil, cfg, store.Chat{Kind: store.KindRoleplay}, one, hist, "")
	a := last[len(last)-1].Content
	if !strings.Contains(a, "Wren's recent messages run to about 7 words, in 1 paragraph.") {
		t.Fatalf("the draft does not say how long Wren writes:\n%s", a)
	}
}

func TestHistoryLeavesOutHiddenAndKnowsWhereTurnsEnd(t *testing.T) {
	msgs := []store.Message{
		{ID: 1, Role: ollama.RoleUser, Content: "Hello."},
		{ID: 2, Role: ollama.RoleAssistant, Content: "Out of character aside.", Hidden: true},
		{ID: 3, Role: ollama.RoleAssistant, Content: "*Vesper nods.*", CharacterID: 7},
		{ID: 4, Role: ollama.RoleAssistant, Content: "*Ilse shrugs.*", CharacterID: 8},
		{ID: 5, Role: ollama.RoleUser, Content: "Right."},
	}
	names := map[int64]string{7: "Vesper", 8: "Ilse"}
	out, ids := HistoryWithIDs(msgs, func(id int64) string { return names[id] })
	if len(out) != 3 || strings.Contains(out[1].Content, "aside") {
		t.Fatalf("history %+v", out)
	}
	if want := []int64{1, 4, 5}; len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("ids %v, want %v", ids, want)
	}
}

func TestUsageAddsUpTheRequest(t *testing.T) {
	cfg := store.DefaultConfig()
	cfg.NumCtx = 8192
	one := []chars.Character{{Name: "Vesper", Description: "A cartographer."}}
	hist := []ollama.Message{{Role: ollama.RoleUser, Content: strings.Repeat("I wait. ", 100)}}
	u := MeasureUsage(nil, cfg, store.Chat{Kind: store.KindRoleplay, Setting: "Her map room, midnight"}, one, hist)
	if u.Window != 8192 || u.Used == 0 || u.Conversation == 0 || u.FoldsAt == 0 {
		t.Fatalf("usage %+v", u)
	}
	sum := 0
	names := []string{}
	for _, p := range u.Parts {
		sum += p.Tokens
		names = append(names, p.Name)
	}
	if sum != u.Used || !strings.Contains(strings.Join(names, ","), "Closing Rules") {
		t.Fatalf("parts %v add to %d, used %d", names, sum, u.Used)
	}
}
