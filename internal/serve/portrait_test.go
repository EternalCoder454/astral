package serve

import (
	"bytes"
	"image"
	"image/png"
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

// A character's picture reaches the phone as a small square, and a chat is
// found by what was said in it.
func TestAvatarAndSearchForThePhone(t *testing.T) {
	s, st := testServer(t)
	tok := paired(t, s)

	pic := filepath.Join(t.TempDir(), "face.png")
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	os.WriteFile(pic, buf.Bytes(), 0o600)
	id, _ := st.SaveCharacter(chars.Character{Name: "Vesper", AvatarPath: pic})
	ch, _ := st.NewChat(id, "The harbour", "m", store.KindRoleplay)
	st.AddMessage(store.Message{ChatID: ch.ID, Role: "assistant", Content: `"The lighthouse keeper owes me a favour."`})

	state := do(t, s, "GET", "/api/state", tok, "").Body.String()
	if !strings.Contains(state, `"avatar":true`) || !strings.Contains(state, `"character":`+strconv.FormatInt(id, 10)) {
		t.Errorf("the state does not say who has a picture:\n%s", state)
	}
	w := do(t, s, "GET", "/api/characters/"+strconv.FormatInt(id, 10)+"/avatar", tok, "")
	if w.Code != http.StatusOK || w.Body.Len() == 0 || w.Body.Len() >= buf.Len()*4 {
		t.Errorf("avatar = %d, %d bytes", w.Code, w.Body.Len())
	}
	if c, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes())); err != nil || c.Width > avatarSize || c.Height > avatarSize {
		t.Errorf("the avatar is %dx%d (%v), want at most %d square", c.Width, c.Height, err, avatarSize)
	}
	found := do(t, s, "GET", "/api/search?q=lighthouse", tok, "").Body.String()
	if !strings.Contains(found, `"id":`+strconv.FormatInt(ch.ID, 10)) || !strings.Contains(found, "lighthouse") {
		t.Errorf("search did not find the chat by what was said in it:\n%s", found)
	}
}
