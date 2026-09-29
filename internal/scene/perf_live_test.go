package scene

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// What one turn of a long scene costs, measured the way the app plays it:
// the prompt built from the store, the reply through Stream, and the setting
// line brought up to date after it. The numbers that matter are the prompt
// tokens the server actually had to read (the cached prefix is free), the time
// before the first words are shown, and how long the work between turns takes.

// sceneSentences are the stuff of a seeded transcript: varied enough that the
// history reads as a scene rather than one sentence repeated.
var sceneSentences = []string{
	`*She rolls the chart flat with the heel of her hand and pins the corners with whatever is nearest.*`,
	`"The tide tables are wrong again, and nobody at the harbour office will say why."`,
	`*Rain works at the skylight, a steady needling that has not stopped since noon.*`,
	`"You keep asking about the Gannet as if I owe you an answer."`,
	`*The lamp gutters; she trims the wick without looking at it, the way a person does a thing done a thousand times.*`,
	`"Kestrel Bay has three piers and two of them are lies."`,
	`*Somewhere below, a door bangs and a voice calls for the harbourmaster, twice, then gives up.*`,
	`"If the Guild finds out I drew the Sever, they will not bother with a trial."`,
	`*She taps the brass dividers against the table edge, once, twice, a small metronome of impatience.*`,
	`"Sit, or leave. Standing in my doorway is neither."`,
}

func seededScene(rng *rand.Rand, turns int) []store.Message {
	var msgs []store.Message
	for i := 0; i < turns; i++ {
		msgs = append(msgs, store.Message{Role: ollama.RoleUser,
			Content: `*I lean on the rail.* "` + []string{"Tell me about the tide.", "Who drew this?", "And the Guild?", "I'm staying."}[i%4] + `"`})
		var b strings.Builder
		for j := 0; j < 7; j++ {
			b.WriteString(sceneSentences[rng.Intn(len(sceneSentences))])
			if j%2 == 1 {
				b.WriteString("\n\n")
			} else {
				b.WriteString(" ")
			}
		}
		msgs = append(msgs, store.Message{Role: ollama.RoleAssistant, Content: strings.TrimSpace(b.String())})
	}
	return msgs
}

func TestLiveTurnCost(t *testing.T) {
	client, model := designModel(t)
	st := liveStore(t)
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	cfg.PersonaName = livePlayer
	cfg.NumCtx = 16384
	rep := newReport(t, "turncost.txt")
	w := saltgrave(t, st)
	c := odile
	c.WorldID = w.ID
	id, err := st.SaveCharacter(c)
	if err != nil {
		t.Fatal(err)
	}
	c.ID = id
	turns := []string{
		`*I shake the rain off.* "The Sever is closed again. Who closed it?"`,
		`"Tell me what the harbourmaster pays you."`,
		`*I put the chart back on the table.* "Show me the drowned market."`,
		`"The Guild will come here tonight. You know that."`,
		`*I wait for her to answer.*`,
	}

	for _, arm := range []string{"with the setting tracked", "without"} {
		ch, err := st.NewChat(c.ID, "Perf", model, store.KindRoleplay)
		if err != nil {
			t.Fatal(err)
		}
		ch.SettingAuto = arm != "without"
		st.SetChatSettingAuto(ch.ID, ch.SettingAuto)
		for _, m := range seededScene(rand.New(rand.NewSource(7)), 40) {
			m.ChatID = ch.ID
			st.AddMessage(m)
		}
		rep.printf("\n######## %s\n", arm)
		for i, u := range turns {
			st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleUser, Content: u})
			ch, _ = st.Chat(ch.ID)
			stored, _ := st.MessagesAfter(ch.ID, ch.SummaryUpto)
			hist := History(stored, nil)
			built := time.Now()
			msgs := BuildFor(st, cfg, ch, []chars.Character{c}, hist)
			buildTook := time.Since(built)

			var firstRaw, firstShown time.Duration
			start := time.Now()
			var last ollama.Stats
			requests := 0
			chat := func(ctx context.Context, m []ollama.Message, d func(ollama.Delta)) (ollama.Message, ollama.Stats, error) {
				requests++
				no := false
				msg, stats, err := client.Chat(ctx, model, m, Options(cfg), &no, func(x ollama.Delta) {
					if firstRaw == 0 && x.Content != "" {
						firstRaw = time.Since(start)
					}
					d(x)
				})
				last = stats
				return msg, stats, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			msg, stats, err := Stream(ctx, chat, ch.Kind, msgs, func(d ollama.Delta) {
				if firstShown == 0 && d.Content != "" {
					firstShown = time.Since(start)
				}
			})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			total := time.Since(start)
			st.AddMessage(store.Message{ChatID: ch.ID, Role: ollama.RoleAssistant, Content: msg.Content})
			line := "turn %d: build %v, prompt read %d tok in %v (last request), first token %v, first shown %v, reply %d tok at %.0f tok/s, %d requests, total %v"
			args := []any{i + 1, buildTook.Round(time.Millisecond), last.PromptTokens, last.PromptElapsed.Round(time.Millisecond),
				firstRaw.Round(time.Millisecond), firstShown.Round(time.Millisecond), stats.Tokens, last.TokPerSec, requests, total.Round(time.Millisecond)}
			rep.printf(line+"\n", args...)
			t.Logf("%s "+line, append([]any{arm}, args...)...)

			if ch.SettingAuto {
				ch, _ = st.Chat(ch.ID)
				stored, _ = st.MessagesAfter(ch.ID, ch.SummaryUpto)
				hist = History(stored, nil)
				ts := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				_, sst, err := client.Structured(ctx, model, StateMessages(st, cfg, ch, []chars.Character{c}, hist, false),
					StateOptions(cfg, ch.Kind), chars.StateSchema(false))
				cancel()
				if err == nil {
					t.Logf("%s   state: prompt read %d tok in %v, total %v", arm, sst.PromptTokens,
						sst.PromptElapsed.Round(time.Millisecond), time.Since(ts).Round(time.Millisecond))
				}
			}
		}
	}
}

// The first reply after the model was unloaded: nothing done while typing,
// today's preload, and a warm-up that loads the model with the scene's own
// settings and reads the scene's prompt so its prefix is cached.
func TestLiveColdStart(t *testing.T) {
	client, model := designModel(t)
	st := liveStore(t)
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	cfg.PersonaName = livePlayer
	cfg.NumCtx = 16384
	w := saltgrave(t, st)
	c := odile
	c.WorldID = w.ID
	id, _ := st.SaveCharacter(c)
	c.ID = id
	ch, _ := st.NewChat(c.ID, "Cold", model, store.KindRoleplay)
	for _, m := range seededScene(rand.New(rand.NewSource(11)), 40) {
		m.ChatID = ch.ID
		st.AddMessage(m)
	}
	cast := []chars.Character{c}
	unload := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		client.Unload(ctx, model)
		cancel()
		time.Sleep(3 * time.Second)
	}
	reply := func(note string, prep func()) {
		unload()
		ps := time.Now()
		if prep != nil {
			prep()
		}
		prepTook := time.Since(ps)
		stored, _ := st.MessagesAfter(ch.ID, 0)
		hist := History(stored, nil)
		hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: `"So where does that leave us?"`})
		msgs := BuildFor(st, cfg, ch, cast, hist)
		start := time.Now()
		var first time.Duration
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		no := false
		_, stats, err := client.Chat(ctx, model, msgs, Options(cfg), &no, func(d ollama.Delta) {
			if first == 0 && d.Content != "" {
				first = time.Since(start)
			}
		})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%-34s prep %6v, then first token %6v (prompt %d tok in %v)", note, prepTook.Round(10*time.Millisecond),
			first.Round(10*time.Millisecond), stats.PromptTokens, stats.PromptElapsed.Round(10*time.Millisecond))
	}
	reply("nothing while typing", nil)
	reply("WarmForTyping", func() {
		stored, _ := st.MessagesAfter(ch.ID, 0)
		msgs := BuildFor(st, cfg, ch, cast, History(stored, nil))
		WarmForTyping(client, model, "cold", msgs, Options(cfg))
	})
}
