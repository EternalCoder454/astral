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

// Whether a scene's reply length is kept to, and whether a character who came
// late keeps out of what they missed, measured on a real model.
func TestLiveLengthAndArrivals(t *testing.T) {
	client, model := designModel(t)
	client.KeepAlive = os.Getenv("ASTRAL_TEST_KEEP_ALIVE")
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	cfg.PersonaName = livePlayer
	runs := liveRuns()
	rep := newReport(t, "length.txt")
	ask := func(msgs []ollama.Message, opts ollama.Options) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		no := false
		msg, _, err := client.Chat(ctx, model, msgs, opts, &no, nil)
		if err != nil {
			t.Fatalf("the model failed: %v", err)
		}
		_, reply := ollama.SplitThinking(msg.Content)
		return strings.TrimSpace(reply)
	}
	only := os.Getenv("ASTRAL_TEST_ONLY")

	if only == "" || only == "length" {
		st := liveStore(t)
		hist := steerScene(Persona(cfg))
		for _, length := range []string{"", chars.LengthShort, chars.LengthMedium, chars.LengthLong} {
			ch := store.Chat{Kind: store.KindRoleplay, ReplyLength: length}
			var words, paras []int
			for r := 0; r < runs*3; r++ {
				reply := ask(BuildFor(st, cfg, ch, []chars.Character{odile}, hist), OptionsFor(cfg, ch.Kind))
				words = append(words, len(strings.Fields(reply)))
				paras = append(paras, len(strings.Split(strings.TrimSpace(reply), "\n\n")))
				rep.printf("\n--- %q\n%s\n", length, reply)
			}
			line := fmt.Sprintf("Length %q: words %v, paragraphs %v", length, words, paras)
			rep.printf("\n== %s\n", line)
			t.Log(line)
		}
	}

	if only == "" || only == "arrivals" {
		knows := regexp.MustCompile(`(?i)\b(papers?|signed|guild)\b`)
		for _, late := range []bool{true, false} {
			st := liveStore(t)
			odileID, _ := st.SaveCharacter(odile)
			ida := chars.Character{Name: "Ida", Description: "A dock hand who drinks at the Lamp and Anchor and hears everything said on the quay, but only on the quay.",
				Personality: "loud, blunt, honest to a fault"}
			idaID, _ := st.SaveCharacter(ida)
			ch, _ := st.NewChat(odileID, "Bar", model, store.KindRoleplay)
			if !late {
				st.SetCast(ch.ID, []int64{odileID, idaID})
			}
			add := func(role, text string, who int64) {
				st.AddMessage(store.Message{ChatID: ch.ID, Role: role, Content: text, CharacterID: who})
			}
			add(ollama.RoleAssistant, chars.Greeting(odile, Persona(cfg)), odileID)
			add(ollama.RoleUser, `*I lean over the bar and keep my voice down.* "I signed the guild's papers this morning. The bar goes to them at the new moon. Don't tell a soul."`, 0)
			add(ollama.RoleAssistant, `*Odile sets the glass down very carefully.* "You what." *She looks at the door, then back at you.* "Not a word. Not here."`, odileID)
			if late {
				st.SetCast(ch.ID, []int64{odileID, idaID})
			}
			add(ollama.RoleUser, `*The door bangs open and Ida stamps in out of the rain.* "Ida. What have you heard about me tonight?"`, 0)
			ch, _ = st.Chat(ch.ID)
			cast, _ := st.Cast(ch.ID)
			stored, _ := st.MessagesAfter(ch.ID, 0)
			names := map[int64]string{odileID: odile.Name, idaID: "Ida"}
			hist := History(stored, func(id int64) string { return names[id] })
			var n, knew int
			for r := 0; r < runs*4; r++ {
				reply := ask(BuildFor(st, cfg, ch, cast, hist), OptionsFor(cfg, ch.Kind))
				n++
				// Only what Ida says: the beat that starts with her name.
				idaPart := reply
				if i := strings.Index(reply, "Ida:"); i >= 0 {
					idaPart = reply[i:]
					if j := strings.Index(idaPart[4:], ":"); j > 0 && strings.Contains(idaPart[4:4+j], "\n") {
						idaPart = idaPart[:4+j]
					}
				}
				if knows.MatchString(idaPart) {
					knew++
				}
				rep.printf("\n--- late %v\n%s\n", late, reply)
			}
			line := fmt.Sprintf("Arrivals, Ida came late %v: %d replies, %d where Ida speaks of the papers", late, n, knew)
			rep.printf("\n== %s\n", line)
			t.Log(line)
		}
	}
}
