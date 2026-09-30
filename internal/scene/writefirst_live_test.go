package scene

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// Whether a character asked to write first after a silence does so without
// writing the person's side, measured on a real model, beside the same turn
// asked for with no word about the silence.
func TestLiveWriteFirst(t *testing.T) {
	client, model := designModel(t)
	client.KeepAlive = os.Getenv("ASTRAL_TEST_KEEP_ALIVE")
	st := liveStore(t)
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	cfg.PersonaName = livePlayer
	rep := newReport(t, "writefirst.txt")
	runs := liveRuns()
	hist := steerScene(Persona(cfg))
	ch := store.Chat{Kind: store.KindRoleplay}
	one := []chars.Character{odile}

	// The person's side written for them: their words, or them doing
	// something, in narration or on a line of their own.
	verbs := `(say|says|said|nod|nods|nodded|lean|leans|leaned|take|takes|took|look|looks|looked|shrug|shrugs|` +
		`smile|smiles|smiled|sigh|sighs|reach|reaches|pick|picks|turn|turns|laugh|laughs|answer|answers|reply|replies|` +
		`slide|slides|push|pushes|pocket|pockets|drink|drinks|finish|finishes)`
	forUser := regexp.MustCompile(`(?i)(\*[^*]*\b(` + livePlayer + `|you)\s+` + verbs + `\b[^*]*\*|(^|\n)\s*` + livePlayer + `:)`)

	for _, arm := range []struct {
		name string
		turn Turn
	}{
		{"writing first", Turn{Nudge: true}},
		{"carrying on, no word of the silence", Turn{}},
	} {
		var n, wrote, words int
		for r := 0; r < runs*4; r++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			no := false
			msg, _, err := client.Chat(ctx, model, BuildTurn(st, cfg, ch, one, hist, arm.turn), OptionsFor(cfg, ch.Kind), &no, nil)
			cancel()
			if err != nil {
				t.Fatalf("the model failed: %v", err)
			}
			_, reply := ollama.SplitThinking(msg.Content)
			reply = strings.TrimSpace(reply)
			n++
			words += len(strings.Fields(reply))
			if forUser.MatchString(reply) {
				wrote++
			}
			rep.printf("\n--- %s\n%s\n", arm.name, reply)
		}
		line := fmt.Sprintf("%s: %d replies, mean %d words, %d wrote for %s", arm.name, n, words/max(n, 1), wrote, livePlayer)
		rep.printf("\n== %s\n", line)
		t.Log(line)
	}
}
