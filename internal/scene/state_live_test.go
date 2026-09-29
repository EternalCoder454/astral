package scene

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// Whether the scene's state is kept right, and what it costs, is not something
// reading the prompt can settle. A scripted scene changes what people wear,
// what they hold, how they stand with each other and where they are, one
// exchange at a time, and the tracker is asked after each. Then the same
// next turn is written with the state sent and without it, to see whether a
// model handed a record of the room starts describing the room every reply.
func TestLiveSceneState(t *testing.T) {
	client, model := designModel(t)
	client.KeepAlive = os.Getenv("ASTRAL_TEST_KEEP_ALIVE")
	st := liveStore(t)
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	cfg.PersonaName = livePlayer
	if n, err := strconv.Atoi(os.Getenv("ASTRAL_TEST_NUM_CTX")); err == nil && n > 0 {
		cfg.NumCtx = n
	}
	rep := newReport(t, "state.txt")
	runs := liveRuns()
	p := Persona(cfg)
	one := []chars.Character{odile}

	type stage struct {
		user, reply string
		expect      map[string]*regexp.Regexp
		// gone is what a part must no longer say.
		gone map[string]*regexp.Regexp
	}
	re := regexp.MustCompile
	stages := []stage{
		{`*I shake the rain off my hat and keep my coat on.* "Quiet night."`,
			`*Odile sets a glass down in front of you and fills it two fingers deep, wiping her hands on her apron.* "Every night's quiet since the navy left. Drink that before you tell me what you want."`,
			map[string]*regexp.Regexp{"where": re(`(?i)bar|lamp|anchor|tavern|counter`)}, nil},
		{`*I take off my wet coat and hang it by the door.* "Keep the stove going?"`,
			`*She throws another log in the stove, then unties her apron and drops it on the counter.* "Stove's going. Bar's closed. Sit."`,
			map[string]*regexp.Regexp{"wearing": re(`(?i)hang|hung|hook|door|off|removed|without`)}, nil},
		{`*I slide a sealed envelope across the bar.* "From the harbour guild. For you."`,
			`*She reads it once, folds it in half and tucks it into her waistcoat.* "They want the bar by the new moon." *Her jaw sets.* "They'll wait."`,
			map[string]*regexp.Regexp{"holding": re(`(?i)letter|envelope`), "unresolved": re(`(?i)guild|moon|bar`)}, nil},
		{`*I look away.* "I already told them you'd sign."`,
			`*She goes very still.* "You what." *She sets the bottle down hard enough to slop it.* "Get out from behind my bar."`,
			map[string]*regexp.Regexp{"relationship": re(`(?i)anger|angry|betray|cold|furious|tense|hostil|distrust|broken|fury|hurt|agitat|confront|shock`)}, nil},
		{`*I follow her out into the back yard, into the rain.* "Odile. Listen to me."`,
			`*She stops by the woodpile, arms folded, rain running off her cropped hair, and lights a cigarette.* "You've got until I finish this."`,
			map[string]*regexp.Regexp{"where": re(`(?i)yard|outside|woodpile|back|rain`)}, nil},
		{`*I tell her everything: the guild's offer, the money, why I said yes.*`,
			`*She drops the cigarette, grinds it out under her boot, and throws the guild's letter into the rain barrel.* "Then we fight them. Together, this time."`,
			map[string]*regexp.Regexp{"relationship": re(`(?i)together|ally|allies|alli|reconcil|trust|united|side|partner`)},
			map[string]*regexp.Regexp{"holding": re(`(?i)letter|envelope|cigarette`)}},
	}

	var tracked, right, total int
	var spent time.Duration
	var final store.Chat
	for r := 0; r < runs; r++ {
		ch := store.Chat{Kind: store.KindRoleplay, SettingAuto: true}
		hist := []ollama.Message{{Role: ollama.RoleAssistant, Content: chars.Greeting(odile, p)}}
		for i, s := range stages {
			hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: s.user},
				ollama.Message{Role: ollama.RoleAssistant, Content: s.reply})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			start := time.Now()
			raw, stats, err := client.Structured(ctx, model, StateMessages(st, cfg, ch, one, hist, false),
				StateOptions(cfg, ch.Kind), chars.StateSchema(false))
			took := time.Since(start)
			cancel()
			if err != nil {
				t.Fatalf("tracking stage %d: %v", i+1, err)
			}
			tracked++
			spent += took
			setting, state, changed := chars.ApplyState(raw, ch.Setting, ch.State)
			ch.Setting, ch.State = setting, state
			rep.printf("\n--- run %d, stage %d: %v, read %d tok, wrote %d tok, changed %v\nanswer %s\nwhere: %s\n%+v\n",
				r+1, i+1, took.Round(time.Millisecond), stats.PromptTokens, stats.Tokens, changed, raw, setting, state)
			t.Logf("run %d stage %d (%v, %d tok out): where %q | %+v", r+1, i+1, took.Round(time.Millisecond), stats.Tokens, setting, state)
			for key, bad := range s.gone {
				total++
				if got := state.Get(key); bad.MatchString(got) {
					t.Logf("  stage %d %s still says %q", i+1, key, got)
				} else {
					right++
				}
			}
			for key, want := range s.expect {
				total++
				got := setting
				if key != "where" {
					got = state.Get(key)
				}
				if want.MatchString(got) {
					right++
				} else {
					t.Logf("  stage %d %s missed: %q", i+1, key, got)
				}
			}
		}
		final = ch
	}
	line := fmt.Sprintf("Tracking: %d passes, mean %v each; %d of %d expected changes recorded", tracked,
		(spent / time.Duration(max(tracked, 1))).Round(time.Millisecond), right, total)
	rep.printf("\n== %s\n", line)
	t.Log(line)

	// The same next turn, with the state and without it. What is counted is
	// how often a reply names what the state lists, when nothing in the turn
	// asks about it.
	hist := []ollama.Message{{Role: ollama.RoleAssistant, Content: chars.Greeting(odile, p)}}
	for _, s := range stages {
		hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: s.user},
			ollama.Message{Role: ollama.RoleAssistant, Content: s.reply})
	}
	hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: `*I sit down on the chopping block and wait.*`})
	mentions := re(`(?i)\b(apron|waistcoat|letter|envelope|hat|coat)\b`)
	for _, arm := range []struct {
		name string
		ch   store.Chat
	}{
		{"with the state", final},
		{"without it", store.Chat{Kind: store.KindRoleplay}},
	} {
		var n, named, words int
		for r := 0; r < runs*3; r++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			no := false
			msg, _, err := client.Chat(ctx, model, BuildFor(st, cfg, arm.ch, one, hist), OptionsFor(cfg, arm.ch.Kind), &no, nil)
			cancel()
			if err != nil {
				t.Fatalf("writing a reply: %v", err)
			}
			_, reply := ollama.SplitThinking(msg.Content)
			n++
			words += len(strings.Fields(reply))
			named += len(mentions.FindAllString(reply, -1))
			rep.printf("\n--- reply %s\n%s\n", arm.name, reply)
		}
		line := fmt.Sprintf("Replies %s: %d, mean %d words, %d mentions of what the state lists (%.1f a reply)",
			arm.name, n, words/max(n, 1), named, float64(named)/float64(max(n, 1)))
		rep.printf("\n== %s\n", line)
		t.Log(line)
	}
}
