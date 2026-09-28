package ollama

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func filterAll(parts ...string) string {
	var f strayFilter
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(f.Next(p))
	}
	return b.String()
}

func TestAStrayTokenIsTakenOut(t *testing.T) {
	got := filterAll("She stares at you while rain hammers the roof, ", "weight shifting", "重心", " onto her back foot.")
	if got != "She stares at you while rain hammers the roof, weight shifting onto her back foot." {
		t.Errorf("got %q", got)
	}
}

func TestAStrayTokenSplitAcrossDeltas(t *testing.T) {
	// A run can arrive a character at a time; each piece must go.
	got := filterAll("The generator coughs and the floodlights ", "dim ", "重", "心", " again.")
	if strings.ContainsAny(got, "重心") || !strings.Contains(got, "dim again.") {
		t.Errorf("got %q", got)
	}
}

func TestFullWidthPunctuationBecomesOrdinary(t *testing.T) {
	got := filterAll("She waited for the answer that never came，then she laughed。")
	if got != "She waited for the answer that never came,then she laughed." {
		t.Errorf("got %q", got)
	}
}

func TestTheOpeningWordsAreLeftAlone(t *testing.T) {
	// Too little English yet to call anything stray: a reply that opens in
	// another script is a different problem, and emptying it helps nobody.
	if got := filterAll("你好, Wren."); got != "你好, Wren." {
		t.Errorf("got %q", got)
	}
}

func TestAConversationInChineseIsNotGuarded(t *testing.T) {
	if needsScriptGuard([]Message{{Role: RoleUser, Content: "你好，今天怎么样？"}}) {
		t.Error("a conversation written in Chinese was guarded")
	}
	if needsScriptGuard([]Message{{Role: RoleSystem, Content: "You are 美咲, a courier."}}) {
		t.Error("a character with a name in kanji was guarded")
	}
	if !needsScriptGuard([]Message{{Role: RoleUser, Content: "Hey, Dag."}}) {
		t.Error("an English conversation was not guarded")
	}
}

func streamServer(t *testing.T, lines ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTheStreamIsFilteredEndToEnd(t *testing.T) {
	srv := streamServer(t,
		`{"message":{"role":"assistant","content":"She shifts her weight onto her back foot, "},"done":false}`,
		`{"message":{"role":"assistant","content":"重心"},"done":false}`,
		`{"message":{"role":"assistant","content":" steady."},"done":false}`,
		`{"message":{"role":"assistant","content":""},"done":true}`)
	c := NewClient(srv.URL)
	var streamed strings.Builder
	msg, _, err := c.Chat(context.Background(), "m", []Message{{Role: RoleUser, Content: "Hey."}}, Options{}, nil,
		func(d Delta) { streamed.WriteString(d.Content) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg.Content, "重") || strings.Contains(streamed.String(), "重") {
		t.Errorf("stored %q, streamed %q", msg.Content, streamed.String())
	}
}

func TestALoopAbortKeepsWhatCameBefore(t *testing.T) {
	srv := streamServer(t,
		`{"message":{"role":"assistant","content":"*She kicks the generator.* \"Useless thing.\""},"done":false}`,
		`{"error":"prediction aborted, token repeat limit reached"}`)
	c := NewClient(srv.URL)
	msg, _, err := c.Chat(context.Background(), "m", []Message{{Role: RoleUser, Content: "Hey."}}, Options{}, nil, nil)
	if !errors.Is(err, ErrRepeatLimit) {
		t.Fatalf("want ErrRepeatLimit, got %v", err)
	}
	if !strings.Contains(msg.Content, "Useless thing.") {
		t.Errorf("the text before the loop was lost: %q", msg.Content)
	}
}

func TestARemovedWordBetweenSpacesLeavesOneSpace(t *testing.T) {
	got := filterAll("She shifts her weight onto the back foot and ", "重心", " waits.")
	if got != "She shifts her weight onto the back foot and waits." {
		t.Errorf("got %q", got)
	}
}
