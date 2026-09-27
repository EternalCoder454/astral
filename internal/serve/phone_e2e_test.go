package serve

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"astral/internal/chars"
	"astral/internal/world"
)

// What the Android app loads is this server's pages and this server's API. The
// WebView cannot be tested here, but everything it fetches can, and a page that
// references a script the server does not serve is the failure that would look
// like the app being broken.
func TestPhoneServesWhatTheAppLoads(t *testing.T) {
	s, _ := testServer(t)
	token := paired(t, s)

	// The page itself, unauthenticated, because the WebView loads it before it
	// has a token and then pairs from inside it.
	index := do(t, s, "GET", "/", "", "")
	if index.Code != http.StatusOK {
		t.Fatalf("the page answered %d", index.Code)
	}
	html := index.Body.String()
	for _, want := range []string{"app.js", "style.css", "cast-characters", "cast-worlds"} {
		if !strings.Contains(html, want) {
			t.Errorf("the page does not reference %q", want)
		}
	}

	// Every asset it references has to be served, or the app opens blank.
	for _, path := range []string{"/app.js", "/style.css"} {
		got := do(t, s, "GET", path, "", "")
		if got.Code != http.StatusOK {
			t.Errorf("%s answered %d", path, got.Code)
		}
		if got.Body.Len() == 0 {
			t.Errorf("%s came back empty", path)
		}
	}

	// The swipe-to-delete code and the endpoints it calls have to agree.
	js := do(t, s, "GET", "/app.js", "", "").Body.String()
	for _, want := range []string{"/api/characters/", "/api/worlds/", "swipeable", "pointerdown"} {
		if !strings.Contains(js, want) {
			t.Errorf("the script is missing %q", want)
		}
	}

	// And the state the page renders from.
	charID, err := s.store.SaveCharacter(chars.Character{Name: "Vesper", Description: "A cartographer."})
	if err != nil {
		t.Fatal(err)
	}
	worldID, err := s.store.SaveWorld(world.World{Name: "Sever Reach"})
	if err != nil {
		t.Fatal(err)
	}

	state := do(t, s, "GET", "/api/state", token, "")
	if state.Code != http.StatusOK {
		t.Fatalf("state answered %d", state.Code)
	}
	var st struct {
		Characters []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"characters"`
		Worlds []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"worlds"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &st); err != nil {
		t.Fatalf("state is not readable: %v", err)
	}
	if len(st.Characters) != 1 || st.Characters[0].Name != "Vesper" {
		t.Errorf("the cast did not come back: %+v", st.Characters)
	}
	if len(st.Worlds) != 1 {
		t.Errorf("the worlds did not come back: %+v", st.Worlds)
	}

	// The gesture's endpoints, against real rows.
	if got := do(t, s, "DELETE", "/api/characters/"+strconv.FormatInt(charID, 10), token, "").Code; got != http.StatusOK {
		t.Errorf("deleting a character answered %d", got)
	}
	if got := do(t, s, "DELETE", "/api/worlds/"+strconv.FormatInt(worldID, 10), token, "").Code; got != http.StatusOK {
		t.Errorf("deleting a world answered %d", got)
	}
	after := do(t, s, "GET", "/api/state", token, "")
	json.Unmarshal(after.Body.Bytes(), &st)
	if len(st.Characters) != 0 || len(st.Worlds) != 0 {
		t.Errorf("the deletions are not reflected in the state: %+v %+v", st.Characters, st.Worlds)
	}
}
