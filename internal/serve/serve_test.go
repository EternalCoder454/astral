package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astral/internal/ollama"
	"astral/internal/store"
)

func testServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := store.DefaultConfig()
	s := New(st, func() store.Config { return cfg },
		func() *ollama.Client { return ollama.NewClient("http://127.0.0.1:1") })
	return s, st
}

func do(t *testing.T, s *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	return w
}

// Nothing but pairing is reachable without a token. This is the property the
// whole feature rests on: the server holds every transcript on the machine and
// a way to drive its models.
func TestEverythingNeedsAToken(t *testing.T) {
	s, _ := testServer(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/state"},
		{"GET", "/api/chats/1"},
		{"POST", "/api/chats"},
		{"POST", "/api/chats/1/send"},
		{"DELETE", "/api/chats/1"},
	} {
		if got := do(t, s, c.method, c.path, "", "{}").Code; got != http.StatusUnauthorized {
			t.Errorf("%s %s with no token = %d, want 401", c.method, c.path, got)
		}
		if got := do(t, s, c.method, c.path, "not-a-real-token", "{}").Code; got != http.StatusUnauthorized {
			t.Errorf("%s %s with a made-up token = %d, want 401", c.method, c.path, got)
		}
	}
}

func TestPairingHandsOutAWorkingToken(t *testing.T) {
	s, _ := testServer(t)

	// No pairing open: a code cannot work, however right it looks.
	if got := do(t, s, "POST", "/api/pair", "", `{"code":"ABCDEFGH"}`).Code; got != http.StatusForbidden {
		t.Errorf("pairing while closed = %d, want 403", got)
	}

	code, err := s.OpenPairing()
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, s, "POST", "/api/pair", "", `{"code":"`+code+`","name":"Test phone"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("pairing = %d: %s", w.Code, w.Body.String())
	}
	var out struct{ Token string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" {
		t.Fatal("pairing returned no token")
	}
	if got := do(t, s, "GET", "/api/state", out.Token, "").Code; got != http.StatusOK {
		t.Errorf("the new token was refused: %d", got)
	}

	// And the code is spent.
	if got := do(t, s, "POST", "/api/pair", "", `{"code":"`+code+`"}`).Code; got != http.StatusForbidden {
		t.Errorf("the same code paired twice: %d", got)
	}
}

// Revoking a device from the PC has to take effect on the next request, not at
// some later point, or "remove this phone" does not mean what it says.
func TestRevokingADeviceLocksItOutImmediately(t *testing.T) {
	s, st := testServer(t)
	code, _ := s.OpenPairing()
	w := do(t, s, "POST", "/api/pair", "", `{"code":"`+code+`","name":"Test phone"}`)
	var out struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &out)

	devices, err := st.Devices()
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices = %+v, %v", devices, err)
	}
	if devices[0].Name != "Test phone" {
		t.Errorf("device name = %q", devices[0].Name)
	}
	if err := st.DeleteDevice(devices[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := do(t, s, "GET", "/api/state", out.Token, "").Code; got != http.StatusUnauthorized {
		t.Errorf("a revoked device is still allowed in: %d", got)
	}
}

// The interface has to be in the binary. Forgetting to embed it produces a
// server that answers every API call and serves a blank page.
func TestTheInterfaceIsServed(t *testing.T) {
	s, _ := testServer(t)
	w := do(t, s, "GET", "/", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d", w.Code)
	}
	for _, want := range []string{"<title>Astral</title>", "app.js", "style.css"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("the page served does not mention %q", want)
		}
	}
	for _, path := range []string{"/app.js", "/style.css"} {
		if got := do(t, s, "GET", path, "", "").Code; got != http.StatusOK {
			t.Errorf("GET %s = %d", path, got)
		}
	}
}

// A token is never stored, only its hash, so a copy of the database is not a
// set of live credentials.
func TestTokensAreNotStored(t *testing.T) {
	s, st := testServer(t)
	code, _ := s.OpenPairing()
	w := do(t, s, "POST", "/api/pair", "", `{"code":"`+code+`"}`)
	var out struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &out)

	if _, ok := st.DeviceByToken(out.Token); !ok {
		t.Fatal("the token does not resolve to its device")
	}
	if _, ok := st.DeviceByToken(out.Token + "x"); ok {
		t.Error("a token with a character added still resolved")
	}
	if store.HashToken(out.Token) == out.Token {
		t.Error("the stored form is the token itself")
	}
}
