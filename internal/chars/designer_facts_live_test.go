package chars

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"astral/internal/ollama"
	"astral/internal/prompts"
)

// A design conversation where the person states the plain facts, age, gender,
// race, occupation and looks, and the build is expected to carry every one of
// them into its own field. Measured because a build came back with those
// fields empty although the conversation had them.
func TestLiveBuildKeepsTheFacts(t *testing.T) {
	_, model := liveModel(t)
	// Every request goes through a proxy that counts them, so a build that
	// needed its facts asked for again shows as two.
	var requests atomic.Int32
	upstream, _ := url.Parse(strings.TrimRight(firstNonEmpty(os.Getenv("OLLAMA_HOST"), "http://127.0.0.1:11434"), "/"))
	if !strings.Contains(upstream.Scheme, "http") {
		upstream, _ = url.Parse("http://" + os.Getenv("OLLAMA_HOST"))
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" {
			requests.Add(1)
		}
		proxy.ServeHTTP(w, r)
	}))
	defer srv.Close()
	client := ollama.NewClient(srv.URL)
	// Matching a model that is already loaded, so the test neither reloads it
	// nor cuts short how long it stays.
	client.KeepAlive = os.Getenv("ASTRAL_TEST_KEEP_ALIVE")
	numCtx := 16384
	if n, err := strconv.Atoi(os.Getenv("ASTRAL_TEST_NUM_CTX")); err == nil && n > 0 {
		numCtx = n
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	u := func(s string) ollama.Message { return ollama.Message{Role: ollama.RoleUser, Content: s} }
	a := func(s string) ollama.Message { return ollama.Message{Role: ollama.RoleAssistant, Content: s} }

	// ASTRAL_TEST_STRIP_FACTS builds with the Character Builder as a rewrite
	// that lost its lines about the facts, as an optimized prompt could.
	if os.Getenv("ASTRAL_TEST_STRIP_FACTS") != "" {
		var kept []string
		for _, line := range strings.Split(extractInstruction, "\n") {
			drop := false
			for _, f := range []string{"- age:", "- gender:", "- race:", "- occupation:", "- relationship:"} {
				drop = drop || strings.HasPrefix(line, f)
			}
			if !drop {
				kept = append(kept, line)
			}
		}
		prompts.SetOverrides(map[string]string{promptBuild: strings.Join(kept, "\n")})
		defer prompts.SetOverrides(nil)
	}

	for _, tc := range []struct {
		name    string
		history []ollama.Message
	}{
		{"facts up front", []ollama.Message{
			a(DesignerOpening),
			u("A blacksmith named Brenna Coalfield. She's 27, a half-orc woman, tall and heavy in the shoulders, with burn scars up both forearms, a shaved head and one gold ring through her lower lip."),
			a("A half-orc smith with a shaved head and a lip ring already has a look people will remember. Is her forge in a city where half-orcs are tolerated, or somewhere she had to fight to be allowed to work? And does she talk to customers, or let the hammer do it?"),
			u("A frontier town. She barely talks, short sentences, and she's blunt to the point of rude."),
			a("Blunt and brief gives the model something to act on in every line. What does she want that she cannot get in that town, and who stands in the way?"),
			u("She wants to leave and make blades for a royal guard, but her father's debts tie her to the forge."),
			a("That gives her a want and a chain holding her back, which is friction the scene can use. You have enough for a rounded character now; press \"Create Character\" when you are ready."),
			u("Build it."),
		}},
		{"facts in passing", []ollama.Message{
			a(DesignerOpening),
			u("I want a nervous librarian who is secretly a thief."),
			a("A librarian who steals is a lovely contradiction. What does he steal, books from his own shelves or things from the people who visit? And is he nervous because of the thefts or just by nature?"),
			u("He, well, he's actually a guy in his fifties, an elf, and he steals rare maps from visitors' bags. Nervous by nature. Thin, stooped, round spectacles, ink on his fingers, always in a moth-eaten green cardigan."),
			a("The cardigan and the inky fingers make him easy to see. Does anyone suspect him, and what does he do with the maps once he has them?"),
			u("Nobody suspects him yet. He sells them to a collector in the capital."),
			a("Then the scene has a secret with a buyer at the end of it. You have enough; press \"Create Character\" when you are ready."),
			u("Make him."),
		}},
	} {
		for run := 1; run <= 3; run++ {
			before := requests.Load()
			c, err := BuildFromConversation(ctx, client, model, tc.history, ollama.Options{NumCtx: numCtx})
			if n := requests.Load() - before; n > 1 {
				t.Logf("%s run %d: the facts were asked for again", tc.name, run)
			}
			if err != nil {
				t.Fatalf("%s run %d: %v", tc.name, run, err)
			}
			t.Logf("%s run %d: name %q | age %q | gender %q | race %q | occupation %q\n  appearance %q",
				tc.name, run, c.Name, c.Age, c.Gender, c.Race, c.Occupation, Snip(c.Appearance, 160))
			for field, v := range map[string]string{"age": c.Age, "gender": c.Gender, "race": c.Race,
				"occupation": c.Occupation, "appearance": c.Appearance} {
				if strings.TrimSpace(v) == "" {
					t.Errorf("%s run %d: %s came back empty", tc.name, run, field)
				}
			}
		}
	}
}

// Snip shortens s for a log line.
func Snip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
