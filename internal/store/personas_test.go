package store

import (
	"path/filepath"
	"testing"

	"astral/internal/chars"
)

func TestPersonasRoundTrip(t *testing.T) {
	st, _, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.SavePersona(chars.Profile{Name: "Wren", Age: "27", Race: "elf", Details: "Owes the guild."})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := st.SavePersona(chars.Profile{Name: "Brand"})
	ch, _ := st.NewChat(0, "Old scene", "m", KindRoleplay)
	if err := st.AdoptOldChats(id); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Chat(ch.ID)
	if got.PersonaID != id {
		t.Fatalf("an old chat was not given the first persona: %d", got.PersonaID)
	}
	st.SetChatPersona(ch.ID, other)
	if err := st.DeletePersona(other); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Chat(ch.ID)
	if got.PersonaID != 0 {
		t.Errorf("a chat kept a deleted persona: %d", got.PersonaID)
	}
	all, _ := st.Personas()
	if len(all) != 1 || all[0].Race != "elf" || all[0].Details != "Owes the guild." {
		t.Errorf("personas = %+v", all)
	}
}
