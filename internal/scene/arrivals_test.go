package scene

import (
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// A member added partway through a group scene is said to have missed what
// came before, quoting the first thing said after they came; the members who
// were there from the start are not mentioned.
func TestAGroupSceneSaysWhoArrivedLate(t *testing.T) {
	st := memoryStore(t)
	odileID, _ := st.SaveCharacter(chars.Character{Name: "Odile", Description: "A bar owner."})
	idaID, _ := st.SaveCharacter(chars.Character{Name: "Ida", Description: "A dock hand."})
	ch, _ := st.NewChat(odileID, "Bar", "m", store.KindRoleplay)
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: "*I lean in.* \"The guild wants the bar.\""})
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: "*Odile goes still.* \"Then they'll have to take it.\""})
	if err := st.SetCast(ch.ID, []int64{odileID, idaID}); err != nil {
		t.Fatal(err)
	}
	odile, _ := st.Character(odileID)
	ida, _ := st.Character(idaID)
	cast := []chars.Character{odile, ida}
	ch, _ = st.Chat(ch.ID)

	// Just arrived, nothing said since.
	if got := arrivals(st, ch, cast); len(got) != 1 || got[0].Name != "Ida" || got[0].Since != "" || got[0].Recorded {
		t.Fatalf("just after Ida came: %+v", got)
	}
	st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: "*The door bangs open and Ida stamps in out of the rain.*"})
	got := arrivals(st, ch, cast)
	if len(got) != 1 || !strings.HasPrefix(got[0].Since, "The door bangs open") {
		t.Fatalf("after something was said: %+v", got)
	}
	hist := []ollama.Message{{Role: ollama.RoleUser, Content: "Ida, what did you hear?"}}
	var closing string
	for _, m := range BuildFor(st, store.Config{}, ch, cast, hist) {
		closing = m.Content
	}
	if !strings.Contains(closing, "Ida arrived partway through") || strings.Contains(closing, "Odile arrived") {
		t.Errorf("the closing block does not say who arrived late:\n%s", closing)
	}

	// Keeping the cast keeps when each came.
	if err := st.SetCast(ch.ID, []int64{odileID, idaID}); err != nil {
		t.Fatal(err)
	}
	if got := arrivals(st, ch, cast); len(got) != 1 || !strings.HasPrefix(got[0].Since, "The door bangs open") {
		t.Errorf("setting the same cast again moved Ida's arrival: %+v", got)
	}
}
