package chars

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCardLink(t *testing.T) {
	for in, want := range map[string]string{
		"https://chub.ai/characters/someone/a-card":         "https://avatars.charhub.io/avatars/someone/a-card/chara_card_v2.png",
		"chub.ai/characters/someone/a-card/main":            "https://avatars.charhub.io/avatars/someone/a-card/chara_card_v2.png",
		"https://www.characterhub.org/characters/who/thing": "https://avatars.charhub.io/avatars/who/thing/chara_card_v2.png",
		"https://example.com/cards/vesper.png":              "https://example.com/cards/vesper.png",
	} {
		got, err := CardLink(in)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "file:///etc/passwd", "https://chub.ai/lorebooks/x/y"} {
		if _, err := CardLink(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestFetchCard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card.json":
			w.Write([]byte(`{"spec":"chara_card_v2","data":{"name":"Vesper","description":"A cartographer."}}`))
		case "/page":
			w.Write([]byte("<html>a page</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _, err := FetchCard(context.Background(), srv.URL+"/card.json")
	if err != nil || c.Name != "Vesper" {
		t.Fatalf("got %+v, %v", c, err)
	}
	if _, _, err := FetchCard(context.Background(), srv.URL+"/page"); err == nil || !strings.Contains(err.Error(), "web page") {
		t.Fatalf("a web page: %v", err)
	}
	if _, _, err := FetchCard(context.Background(), srv.URL+"/missing"); err == nil {
		t.Fatal("a missing card should fail")
	}
}
