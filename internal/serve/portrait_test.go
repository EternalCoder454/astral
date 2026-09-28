package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/store"
)

// A character with a portrait has it sent to the phone, behind the token like
// everything else, and a chat without one says so rather than failing.
func TestThePortraitReachesThePhone(t *testing.T) {
	s, st := testServer(t)
	tok := paired(t, s)

	pic := filepath.Join(t.TempDir(), "vesper.png")
	png := []byte("\x89PNG\r\n\x1a\n not really, but enough to be served")
	if err := os.WriteFile(pic, png, 0o600); err != nil {
		t.Fatal(err)
	}
	with, err := st.SaveCharacter(chars.Character{Name: "Vesper", PortraitPath: pic})
	if err != nil {
		t.Fatal(err)
	}
	without, err := st.SaveCharacter(chars.Character{Name: "Maren"})
	if err != nil {
		t.Fatal(err)
	}
	chatWith, _ := st.NewChat(with, "With", "m", store.KindRoleplay)
	chatWithout, _ := st.NewChat(without, "Without", "m", store.KindRoleplay)
	path := func(id int64) string { return "/api/chats/" + strconv.FormatInt(id, 10) }

	if body := do(t, s, "GET", path(chatWith.ID), tok, "").Body.String(); !strings.Contains(body, `"portrait":true`) {
		t.Errorf("the chat does not say it has a portrait:\n%s", body)
	}
	if body := do(t, s, "GET", path(chatWithout.ID), tok, "").Body.String(); strings.Contains(body, `"portrait"`) {
		t.Errorf("a chat with no portrait claims one:\n%s", body)
	}

	w := do(t, s, "GET", path(chatWith.ID)+"/portrait", tok, "")
	if w.Code != http.StatusOK || w.Body.String() != string(png) {
		t.Errorf("the portrait = %d, %d bytes; want the file", w.Code, w.Body.Len())
	}
	if w := do(t, s, "GET", path(chatWithout.ID)+"/portrait", tok, ""); w.Code != http.StatusNotFound {
		t.Errorf("no portrait = %d, want 404", w.Code)
	}
	if w := do(t, s, "GET", path(chatWith.ID)+"/portrait", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the portrait without a token = %d, want 401", w.Code)
	}
}
