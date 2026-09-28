package scene

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// Live tests of steering a scene one turn at a time: Write for Me, Rewrite
// with a Note, and a group's Let Them Talk and chosen speaker. Same rules as
// the other live tests: requests assembled exactly as the window assembles
// them, one at a time, transcripts to ASTRAL_TEST_OUT to be read, and a control
// beside every count.

// steerScene is a short scene with Odile whose turns from Wren are written in
// one recognisable way: first person, present tense, short, one action and one
// line. A draft that matches them is in Wren's voice; one that reads like
// Odile's replies is not.
func steerScene(p chars.Persona) []ollama.Message {
	return []ollama.Message{
		{Role: ollama.RoleAssistant, Content: chars.Greeting(odile, p)},
		{Role: ollama.RoleUser, Content: `*I shake the rain off my hat.* "Quiet night."`},
		{Role: ollama.RoleAssistant, Content: `*Odile sets a glass down in front of you without being asked and fills it two fingers deep.* "Every night's quiet since the navy left. Drink that before you tell me what you want."`},
		{Role: ollama.RoleUser, Content: `*I drink half of it.* "The Gannet came in this morning. Who bought her cargo?"`},
		{Role: ollama.RoleAssistant, Content: `*She wipes the bar in a slow circle, not looking at you.* "Cargo's cargo. Men buy it, men sell it, and none of them tell me their names." *The rag stops.* "Who's asking?"`},
		{Role: ollama.RoleUser, Content: `*I put a coin on the bar and keep my finger on it.* "Someone who pays."`},
		{Role: ollama.RoleAssistant, Content: `*Odile looks at the coin, then at you, and her mouth goes thin.* "Put that away. Coin like that gets a person noticed in here, and noticed people end up in the harbour." *She leans closer.* "Ask me again tomorrow. Not tonight."`},
	}
}

var (
	// herAction is narration of Odile doing something, the thing a draft of
	// Wren's turn must never write.
	herAction = regexp.MustCompile(`\*(?:She|Odile)\b[^*]*\*`)
	firstPers = regexp.MustCompile(`\bI\b`)
	quoted    = regexp.MustCompile(`"[^"]*"|“[^”]*”`)
)

func dialogueShare(s string) float64 {
	total := len(strings.TrimSpace(s))
	if total == 0 {
		return 0
	}
	n := 0
	for _, q := range quoted.FindAllString(s, -1) {
		n += len(q)
	}
	return float64(n) / float64(total)
}

func TestLiveSteering(t *testing.T) {
	client, model := designModel(t)
	st := liveStore(t)
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	cfg.PersonaName = livePlayer
	cfg.PersonaDescription = "A courier in a salt-stained coat who reads the messages they carry."
	rep := newReport(t, "steering.txt")
	only := os.Getenv("ASTRAL_TEST_ONLY")
	controlPrompts(t)
	runs := liveRuns()
	p := Persona(cfg)
	ch := store.Chat{Kind: store.KindRoleplay}
	one := []chars.Character{odile}
	hist := steerScene(p)

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

	// Wren's own turns average this many words; a draft is judged against it.
	userWords := 0
	userTurns := 0
	for _, m := range hist {
		if m.Role == ollama.RoleUser {
			userWords += words(m.Content)
			userTurns++
		}
	}
	rep.printf("Wren's turns average %d words.\n", userWords/userTurns)

	if only == "" || strings.Contains("draft", only) {
		for _, idea := range []string{"", "ask her who is paying her to keep quiet"} {
			var n, label, her, first, long, onIdea, total int
			for r := 0; r < runs*4; r++ {
				raw := ask(Draft(st, cfg, ch, one, hist, idea), DraftOptions(cfg, ch.Kind))
				d := chars.CleanDraft(raw, livePlayer)
				n++
				total += words(d)
				if strings.HasPrefix(raw, livePlayer+":") {
					label++
				}
				if herAction.MatchString(d) {
					her++
				}
				if firstPers.MatchString(d) {
					first++
				}
				if words(d) > 3*userWords/userTurns {
					long++
				}
				if idea != "" && (strings.Contains(strings.ToLower(d), "pay") || strings.Contains(strings.ToLower(d), "quiet")) {
					onIdea++
				}
				rep.printf("\n--- draft, idea %q\n%s\n", idea, d)
			}
			line := "Write for Me, idea %q: %d drafts, mean %d words, %d labelled, %d narrating Odile, %d in the first person, %d over three times Wren's length"
			args := []any{idea, n, total / max(n, 1), label, her, first, long}
			if idea != "" {
				line += ", %d on the idea"
				args = append(args, onIdea)
			}
			rep.printf("\n== "+line+"\n", args...)
			t.Logf(line, args...)
		}
	}

	if only == "" || strings.Contains("suggest", only) {
		var n, three, her, first, you, long, samey, total int
		for r := 0; r < runs*4; r++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			raw, _, err := client.Structured(ctx, model, Suggest(st, cfg, ch, one, hist), SuggestOptions(cfg, ch.Kind), chars.SuggestSchema)
			cancel()
			if err != nil {
				t.Fatalf("the model failed: %v", err)
			}
			opts := chars.ParseSuggestions(raw, livePlayer)
			n++
			if len(opts) == 3 {
				three++
			}
			for i, o := range opts {
				total += words(o)
				if herAction.MatchString(o) {
					her++
				}
				if firstPers.MatchString(o) {
					first++
				}
				if regexp.MustCompile(`\*You\b`).MatchString(o) {
					you++
				}
				if words(o) > 3*userWords/userTurns {
					long++
				}
				for _, other := range opts[i+1:] {
					if overlap(o, other) > 0.6 {
						samey++
					}
				}
				rep.printf("\n--- suggestion %d.%d\n%s\n", r+1, i+1, o)
			}
		}
		line := "Suggest: %d asks, %d gave three, mean %d words, %d narrating Odile, %d in the first person, %d narrating you, %d over three times Wren's length, %d pairs alike"
		rep.printf("\n== "+line+"\n", n, three, total/max(3*n, 1), her, first, you, long, samey)
		t.Logf(line, n, three, total/max(3*n, 1), her, first, you, long, samey)

		for r := 0; r < runs*3; r++ {
			reply := chars.CleanSetting(ask(SuggestSetting(st, cfg, ch, one, hist), DraftOptions(cfg, ch.Kind)))
			rep.printf("\n--- setting\n%s\n", reply)
			t.Logf("Setting: %d words: %s", words(reply), reply)
		}
	}

	if only == "" || strings.Contains("mine", only) {
		// A message typed in a hurry, and a bare note of intent: both should
		// come back as Wren's message, better, meaning the same thing.
		for _, tc := range []struct {
			typed string
			keep  []string
		}{
			{`*i put a coin on teh bar* "tell me who bougth the cargo or i walk"`, []string{"coin", "cargo", "walk"}},
			{`ask her to come with me to the docks tonight`, []string{"dock"}},
		} {
			var n, kept, her, first, long, total int
			for r := 0; r < runs*4; r++ {
				d := chars.CleanDraft(ask(Draft(st, cfg, ch, one, hist, tc.typed), DraftOptions(cfg, ch.Kind)), livePlayer)
				n++
				total += words(d)
				ok := true
				for _, k := range tc.keep {
					if !strings.Contains(strings.ToLower(d), k) {
						ok = false
					}
				}
				if ok {
					kept++
				}
				if herAction.MatchString(d) {
					her++
				}
				if firstPers.MatchString(d) {
					first++
				}
				if words(d) > 3*max(words(tc.typed), userWords/userTurns) {
					long++
				}
				rep.printf("\n--- rewrite mine %q\n%s\n", tc.typed, d)
			}
			line := "Rewrite mine %q: %d, mean %d words, %d keep the meaning, %d narrating Odile, %d in the first person, %d over three times as long"
			rep.printf("\n== "+line+"\n", tc.typed, n, total/max(n, 1), kept, her, first, long)
			t.Logf(line, tc.typed, n, total/max(n, 1), kept, her, first, long)
		}
	}

	if only == "" || strings.Contains("note", only) {
		// The last turn asks for something she might or might not give.
		asked := append(append([]ollama.Message(nil), hist...),
			ollama.Message{Role: ollama.RoleUser, Content: `*I leave the coin where it is.* "Tomorrow might be too late. Tell me now and it's yours."`})
		for _, note := range []string{"", "Shorter", "More Dialogue", "She refuses to tell Wren anything"} {
			var n, total, named int
			var share float64
			for r := 0; r < runs*4; r++ {
				msgs := BuildTurn(st, cfg, ch, one, asked, Turn{Note: note})
				reply := ask(msgs, Options(cfg))
				n++
				total += words(reply)
				share += dialogueShare(reply)
				// A name for the buyer: a capitalised word in her speech
				// that is not one of the scene's own.
				for _, q := range quoted.FindAllString(reply, -1) {
					if regexp.MustCompile(`\b(?:named?|called|it was|it's)\s+[A-Z][a-z]+`).MatchString(q) {
						named++
						break
					}
				}
				rep.printf("\n--- rewrite, note %q\n%s\n", note, reply)
			}
			line := "Rewrite, note %q: %d replies, mean %d words, dialogue share %.2f, %d name a buyer"
			rep.printf("\n== "+line+"\n", note, n, total/max(n, 1), share/float64(max(n, 1)), named)
			t.Logf(line, note, n, total/max(n, 1), share/float64(max(n, 1)), named)
		}
	}

	if only == "" || strings.Contains("group", only) {
		cast := heistCast()
		names := chars.CastNames(cast)
		group := []ollama.Message{
			{Role: ollama.RoleAssistant, Content: chars.Label(cast[0].Name, `*Brannoc doesn't look up from the window.* "You're late. Put them on the table."`)},
			{Role: ollama.RoleUser, Content: `*I unroll the plans and pin the corners with their mugs.* "Tell me why this won't work."`},
			{Role: ollama.RoleAssistant, Content: chars.Label(cast[1].Name, `*Suvi cracks her knuckles over the vault drawing.* "Because that door's a Brenner Seven, and nobody's opened one in under an hour."`) +
				"\n\n" + chars.Label(cast[0].Name, `*Brannoc finally turns.* "Then we have an hour."`)},
		}
		for _, tc := range []struct {
			name  string
			turn  Turn
			extra string
		}{
			{"let them talk", Turn{Onward: true}, ""},
			{"control, carrying on with no note", Turn{}, ""},
			{"Brannoc answers", Turn{Speaker: "Brannoc Teague"}, `"Does anyone else think this is a bad idea?"`},
			{"control, nobody chosen", Turn{}, `"Does anyone else think this is a bad idea?"`},
		} {
			h := append([]ollama.Message(nil), group...)
			if tc.extra != "" {
				h = append(h, ollama.Message{Role: ollama.RoleUser, Content: tc.extra})
			}
			var n, first, brannoc, wren, askWren int
			for r := 0; r < runs*4; r++ {
				reply := ask(BuildTurn(st, cfg, ch, cast, h, tc.turn), Options(cfg))
				beats := chars.SplitBeats(reply, names)
				n++
				if len(beats) > 0 && beats[0].Name == "Ottavio Brisa" {
					first++
				}
				if len(beats) > 0 && beats[0].Name == "Brannoc Teague" {
					brannoc++
				}
				if regexp.MustCompile(`\*` + livePlayer + `\b|\bI (?:say|nod|shrug)`).MatchString(reply) {
					wren++
				}
				// A line put to the person who is not there: a question with
				// "you" in it, which in this cast means Wren.
				for _, q := range quoted.FindAllString(reply, -1) {
					if strings.Contains(q, "?") && regexp.MustCompile(`(?i)\byou\b`).MatchString(q) && tc.extra == "" {
						askWren++
						break
					}
				}
				rep.printf("\n--- %s\n%s\n", tc.name, reply)
			}
			line := "Group, %s: %d replies, %d open with Ottavio, %d with Brannoc, %d narrate Wren, %d ask someone a question"
			rep.printf("\n== "+line+"\n", tc.name, n, first, brannoc, wren, askWren)
			t.Logf(line, tc.name, n, first, brannoc, wren, askWren)
		}
	}
}

// mara is a character who will move a scene along if she is let: forward,
// impulsive, and already interested.
var mara = chars.Character{
	Name: "Mara Kell",
	Description: "A night-shift nurse in her thirties who has been flirting with {{user}} at the same late bar " +
		"for weeks. Warm, teasing and forward; she says what she wants and does not wait to be asked twice.",
	Personality: "warm, teasing, forward, impulsive",
	FirstMes:    `*Mara slides onto the stool next to yours and steals the olive out of your glass.* "Last call's in ten minutes. You going to walk me somewhere, or do I have to ask?"`,
}

// paceCases are moments a reply could run past: each is the person starting
// something, and the pattern is the reply finishing it for them.
var paceCases = []struct {
	name  string
	turn  string
	ahead *regexp.Regexp
}{
	{"we walk to my house", `*We walk to my house.*`,
		regexp.MustCompile(`(?i)\b(the door (?:opens|swings|clicks|shuts)|unlock\w*|step\w* inside|inside (?:the|your)|hallway|living room|couch|bedroom|kiss\w*|making out)\b`)},
	{"back to my place", `"Let's go back to my place." *We head out together.*`,
		regexp.MustCompile(`(?i)\b(the door (?:opens|swings|clicks|shuts)|unlock\w*|step\w* inside|inside (?:the|your)|hallway|living room|couch|bedroom|kiss\w*|making out)\b`)},
	{"walking home", `*I take your hand and we start walking toward my place, a few blocks away.*`,
		regexp.MustCompile(`(?i)\b(front door|the door (?:opens|swings|clicks|shuts)|unlock\w*|step\w* inside|inside (?:the|your)|hallway|living room|couch|bedroom|kiss\w*)\b`)},
	{"heading out", `"Come on, let's get out of here." *I grab my coat and head for the door.*`,
		regexp.MustCompile(`(?i)\b(arriv\w*|we reach\w*|pull\w* up outside|step\w* inside|unlock\w*|bedroom|kiss\w*)\b`)},
	{"on the couch", `*I sit down next to you on the couch, closer than I need to.*`,
		regexp.MustCompile(`(?i)\b(kiss\w*|straddl\w*|unbutton\w*|bedroom|shirt (?:off|comes))\b`)},
}

func TestLivePacing(t *testing.T) {
	client, model := designModel(t)
	st := liveStore(t)
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	cfg.PersonaName = livePlayer
	rep := newReport(t, "pacing.txt")
	controlPrompts(t)
	p := Persona(cfg)
	ch := store.Chat{Kind: store.KindRoleplay}
	one := []chars.Character{mara}
	pace := strings.TrimSpace(chars.PaceBlock(mara.Name, livePlayer))

	// A style that asks for long replies that keep moving, the kind people
	// write for themselves, and a reply limit to match. A short default reply
	// has no room to run ahead in; this is where it happens.
	long := chars.WritingStyle{Name: "Long and Vivid", Instructions: "Length: Five to seven long paragraphs.\n" +
		"Pacing: Keep the story moving. Something should happen in every reply, and the character acts on what they want.\n" +
		"Description: Rich sensory detail, what things feel, smell and sound like.\n" +
		"Dialogue: Natural, flirtatious where it fits."}
	arms := []string{"control, no pace rule", "pace rule"}
	if os.Getenv("ASTRAL_TEST_ARM") == "long" {
		cfg.SetStyle(long)
		cfg.NumPredict = 2048
		p = Persona(cfg)
		arms = []string{"long style, control, no pace rule", "long style, pace rule"}
	}
	for _, arm := range arms {
		total, ahead, slips := 0, 0, 0
		for _, pc := range paceCases {
			hist := []ollama.Message{
				{Role: ollama.RoleAssistant, Content: chars.Greeting(mara, p)},
				{Role: ollama.RoleUser, Content: `"Walk you somewhere? I could be persuaded." *I finish my drink.*`},
				{Role: ollama.RoleAssistant, Content: `*She laughs, low, and tucks a loose strand of hair behind her ear.* "Persuaded. Listen to you." *Her knee bumps yours under the bar and stays there.* "I've had a twelve-hour shift and a very long week, Wren. Persuade me faster."`},
				{Role: ollama.RoleUser, Content: `*I lean in until my mouth is near your ear.* "I've wanted to get you alone for weeks."`},
				{Role: ollama.RoleAssistant, Content: `*Mara goes very still, then turns her head so her lips brush your jaw.* "Then stop wanting and do something about it." *She pulls back just far enough to look at you, eyes dark, and drops a crumpled twenty on the bar.* "I'm done waiting."`},
				{Role: ollama.RoleUser, Content: pc.turn},
			}
			n := 0
			for r := 0; r < liveRuns()*6; r++ {
				msgs := BuildFor(st, cfg, ch, one, hist)
				if strings.Contains(arm, "no pace rule") {
					last := &msgs[len(msgs)-1]
					last.Content = strings.Replace(last.Content, "\n\n"+pace, "", 1)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				no := false
				msg, _, err := client.Chat(ctx, model, msgs, Options(cfg), &no, nil)
				cancel()
				if err != nil {
					t.Fatalf("the model failed: %v", err)
				}
				_, reply := ollama.SplitThinking(msg.Content)
				reply = strings.TrimSpace(reply)
				total++
				mark := ""
				// Her narration in the first person: "I pull my coat", "we
				// step outside", which is the character telling it as herself.
				for _, span := range regexp.MustCompile(`\*[^*]+\*`).FindAllString(reply, -1) {
					if regexp.MustCompile(`\b(I|we|We|our|Our)\b`).MatchString(span) {
						slips++
						mark += "  [FIRST PERSON]"
						break
					}
				}
				if m := pc.ahead.FindString(reply); m != "" {
					ahead++
					n++
					mark = "  [AHEAD: " + m + "]"
				}
				rep.printf("\n--- %s, %s%s\n%s\n", arm, pc.name, mark, reply)
			}
			rep.printf("\n== %s, %s: %d ran ahead\n", arm, pc.name, n)
			t.Logf("%s, %s: %d of %d ran ahead", arm, pc.name, n, liveRuns()*6)
		}
		t.Logf("%s: %d of %d replies ran ahead, %d narrated in the first person", arm, ahead, total, slips)
	}
}
