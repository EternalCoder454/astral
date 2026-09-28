package serve

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// A long chat goes to the phone compressed when it asks for that, and plain
// when it does not; small answers are never worth it.
func TestALongChatIsSentCompressed(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)
	id, _ := s.store.SaveCharacter(chars.Character{Name: "Vesper"})
	ch, _ := s.store.NewChatIn(id, 0, "Long", "m", store.KindRoleplay)
	for i := 0; i < 1500; i++ {
		role := ollama.RoleUser
		if i%2 == 1 {
			role = ollama.RoleAssistant
		}
		s.store.AddMessage(store.Message{ChatID: ch.ID, Role: role,
			Content: `*She rolls the chart flat and pins the corners.* "The tide tables are wrong again, and nobody will say why."`})
	}
	get := func(gz bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/chats/"+itoa(ch.ID), nil)
		r.Header.Set("Authorization", "Bearer "+token)
		if gz {
			r.Header.Set("Accept-Encoding", "gzip")
		}
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		return w
	}
	plain := get(false)
	zipped := get(true)
	if plain.Header().Get("Content-Encoding") != "" || zipped.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("encodings %q and %q", plain.Header().Get("Content-Encoding"), zipped.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(zipped.Body)
	if err != nil {
		t.Fatal(err)
	}
	unzipped, _ := io.ReadAll(zr)
	var out struct{ Messages []json.RawMessage }
	if err := json.Unmarshal(unzipped, &out); err != nil || len(out.Messages) != 1500 {
		t.Fatalf("decoded %d messages, %v", len(out.Messages), err)
	}
	t.Logf("a 1500-message chat: %d KB plain, %d KB compressed", plain.Body.Len()>>10, zipped.Body.Len()>>10)
	if zipped.Body.Len()*3 > plain.Body.Len() {
		t.Errorf("compressed to only %d of %d bytes", zipped.Body.Len(), plain.Body.Len())
	}
	// A small answer is left alone.
	small := httptest.NewRequest("GET", "/api/settings", nil)
	small.Header.Set("Authorization", "Bearer "+token)
	small.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, small)
	if w.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(w.Body.String(), "{") {
		t.Errorf("a small answer was compressed")
	}
}
