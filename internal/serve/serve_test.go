package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		func() *ollama.Client { return ollama.NewClient("http://127.0.0.1:1") },
		func(next store.Config) error { cfg = next; return nil }, "test")
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

// paired is a token for a device, for the tests that need to be let in.
func paired(t *testing.T, s *Server) string {
	t.Helper()
	code, err := s.OpenPairing()
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, s, "POST", "/api/pair", "", `{"code":"`+code+`","name":"Test phone"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("pairing failed: %s", w.Body.String())
	}
	var out struct{ Token string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Token
}

// A phone changes the settings the window is using, not a copy of them.
func TestSettingsRoundTrip(t *testing.T) {
	s, _ := testServer(t)
	tok := paired(t, s)

	w := do(t, s, "POST", "/api/settings", tok,
		`{"persona":"Wren","persona_note":"A courier.","temperature":0.7,"num_ctx":16384}`)
	if w.Code != http.StatusOK {
		t.Fatalf("saving = %d: %s", w.Code, w.Body.String())
	}
	var got settingsOut
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Persona != "Wren" || got.PersonaNote != "A courier." {
		t.Errorf("persona = %q / %q", got.Persona, got.PersonaNote)
	}
	if got.Temperature != 0.7 || got.NumCtx != 16384 {
		t.Errorf("temperature = %v, num_ctx = %d", got.Temperature, got.NumCtx)
	}

	// And it stuck: a fresh read sees it, and so does the home screen.
	var reread settingsOut
	json.Unmarshal(do(t, s, "GET", "/api/settings", tok, "").Body.Bytes(), &reread)
	if reread.Persona != "Wren" {
		t.Errorf("a re-read says %q", reread.Persona)
	}
	if !strings.Contains(do(t, s, "GET", "/api/state", tok, "").Body.String(), "Wren") {
		t.Error("the home screen did not see the change")
	}
}

// A field the phone does not send must be left alone. An older phone talking to
// a newer PC must not be able to blank a setting it has never heard of.
func TestSettingsLeavesUnsentFieldsAlone(t *testing.T) {
	s, _ := testServer(t)
	tok := paired(t, s)

	do(t, s, "POST", "/api/settings", tok, `{"persona":"Wren","num_ctx":16384}`)
	var got settingsOut
	json.Unmarshal(do(t, s, "POST", "/api/settings", tok, `{"persona":"Vesper"}`).Body.Bytes(), &got)
	if got.Persona != "Vesper" {
		t.Errorf("persona = %q, want the new one", got.Persona)
	}
	if got.NumCtx != 16384 {
		t.Errorf("num_ctx = %d, want the 16384 that was not resent", got.NumCtx)
	}
}

// Unpairing from the phone has to lock that phone out, for the one being sold.
func TestAPhoneCanForgetItself(t *testing.T) {
	s, _ := testServer(t)
	tok := paired(t, s)
	if got := do(t, s, "POST", "/api/forget", tok, "{}").Code; got != http.StatusOK {
		t.Fatalf("forget = %d", got)
	}
	if got := do(t, s, "GET", "/api/state", tok, "").Code; got != http.StatusUnauthorized {
		t.Errorf("a phone that forgot itself is still allowed in: %d", got)
	}
}

// The phone draws with the window's icon set, served rather than redrawn. A
// missing file is a blank button, which looks like a broken app.
func TestTheIconSetIsServed(t *testing.T) {
	s, _ := testServer(t)
	for _, name := range []string{"home", "chat", "characters", "settings", "send", "panel-left"} {
		path := "/icons/astral-" + name + "-symbolic.svg"
		w := do(t, s, "GET", path, "", "")
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d", path, w.Code)
			continue
		}
		if !strings.Contains(w.Body.String(), "<svg") {
			t.Errorf("%s is not an svg", path)
		}
	}
}

// Every icon the interface asks for by name has to exist in the set, or that
// button renders as nothing at all and the page looks broken rather than
// missing one picture.
func TestEveryIconTheInterfaceAsksForExists(t *testing.T) {
	s, _ := testServer(t)
	page := do(t, s, "GET", "/", "", "").Body.String()
	found := 0
	for _, part := range strings.Split(page, `data-icon="`)[1:] {
		name := part[:strings.IndexByte(part, '"')]
		found++
		path := "/icons/astral-" + name + "-symbolic.svg"
		if got := do(t, s, "GET", path, "", "").Code; got != http.StatusOK {
			t.Errorf("the page asks for %q and %s is %d", name, path, got)
		}
	}
	if found < 4 {
		t.Fatalf("only found %d icon references; the check is no longer finding them", found)
	}
}

// The version goes into a download URL, so it is checked rather than trusted.
// Nothing a phone sends should be able to make this machine fetch an address
// somebody else chose.
func TestTheDownloadWillNotFetchAnArbitraryAddress(t *testing.T) {
	s, _ := testServer(t)
	tok := paired(t, s)
	for _, bad := range []string{
		"", "latest", "0.3", "0.3.1.2", "../../etc/passwd", "1.2.3/../..",
		"0.3.1%0d%0a", "0.0.0@evil.example.com", "99999.1.1", "a.b.c",
		"0.3.-1", "0.3.1#x",
		// "0.3.1 " is deliberately not here: surrounding space is trimmed
		// before the check, and what is left is a real version.
	} {
		w := do(t, s, "GET", "/api/app/download?version="+url.QueryEscape(bad), tok, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("version %q was accepted (%d), want 400", bad, w.Code)
		}
	}
}

func TestPlausibleVersion(t *testing.T) {
	for _, ok := range []string{"0.3.1", "1.0.0", "10.20.30", "0.0.0"} {
		if !plausibleVersion(ok) {
			t.Errorf("%q was rejected", ok)
		}
	}
	for _, bad := range []string{"", "1", "1.2", "1.2.3.4", "v1.2.3", "1.2.x", "1.2.99999", "1..3"} {
		if plausibleVersion(bad) {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// Asking about an app update still needs a token: it is a route that makes
// this machine go out to the network on a caller's say-so.
func TestAppUpdateRoutesNeedAToken(t *testing.T) {
	s, _ := testServer(t)
	for _, path := range []string{"/api/app/latest?have=0.1.0", "/api/app/download?version=0.3.1"} {
		if got := do(t, s, "GET", path, "", "").Code; got != http.StatusUnauthorized {
			t.Errorf("GET %s with no token = %d, want 401", path, got)
		}
	}
}
