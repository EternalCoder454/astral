package scene

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/websearch"
	"astral/internal/world"
)

// Heavy live tests of every kind of conversation the designers harness next
// door does not cover: scenes (one character, a cast, and a world with nobody
// in particular in it), General Chat, the three revisers and what their builds
// save, the web search guidance as it is actually sent, which is on by default,
// and reading a picture.
//
// Same rules as the designers: the request is assembled exactly as the window
// assembles it, one request at a time, and every transcript goes to
// ASTRAL_TEST_OUT to be read. The counts are the half that can be compared
// between two versions of a prompt; the transcripts are the half that says
// whether a count means anything.
//
// ASTRAL_TEST_ONLY narrows to scripts whose name contains it, ASTRAL_TEST_RUNS
// repeats the whole set, and ASTRAL_TEST_CONTROL swaps in other versions of the
// prompts for a control, as it does for the designers.

func liveRuns() int {
	runs := 1
	if n, err := fmt.Sscan(os.Getenv("ASTRAL_TEST_RUNS"), &runs); n != 1 || err != nil || runs < 1 {
		runs = 1
	}
	return runs
}

func liveStore(t *testing.T) *store.Store {
	t.Helper()
	st, _, err := store.Open(filepath.Join(t.TempDir(), "astral.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// ---------------------------------------------------------------- scenes ---

// The person playing, in every scene below. Named so a reply that calls them
// by name, or writes a line as them, can be counted.
const livePlayer = "Wren"

type sceneScript struct {
	name  string
	kind  string // "one", "group" or "world"
	turns []string
}

var sceneScripts = []sceneScript{
	{"bar at closing", "one", []string{
		`*I push the door shut behind me and take the stool nearest the taps.* "One drink. Then I'm gone."`,
		`"Whiskey. Whatever's cheapest."`,
		`*I wait.*`,
		`"I'm looking for a man called Pell. Word is he drinks here."`,
		`*I slide a folded banknote across the bar toward her.*`,
		`"Why are you still here, Odile? This place is dying."`,
		`*I reach across the bar and take her scarred hand in mine.*`,
		`*The door bangs open. Three men in wet oilskins shoulder in, and the first one has a knife out.*`,
		`*I stay on my stool and say nothing.*`,
		`"I should go. It's late."`,
	}},
	{"cellar interrogation", "one", []string{
		`*I pull against the chains until the chair scrapes on the stone.* "You'll get nothing from me."`,
		`*I spit at her boots.*`,
		`"Go to hell."`,
		`*I say nothing and stare at the wall.*`,
		`"Do it, then. Whatever you're going to do, do it."`,
		`*I scream until my voice gives out.*`,
		`"Fine. Fine! The shipment comes through the north gate. Thursday."`,
		`*I wait.*`,
	}},
	{"heist crew", "group", []string{
		`*I unroll the building plans across the table and pin the corners down with their mugs.* "Right. Tell me why this won't work."`,
		`"Suvi, can you get through the vault door or not?"`,
		`*I wait for somebody to say something useful.*`,
		`"Ottavio, you're sweating. What's wrong?"`,
		`*I put my pistol on the table.* "Anyone who wants out, say it now."`,
		`"We go at four. Questions?"`,
		`*Someone knocks on the door downstairs. Three slow knocks.*`,
		`"Nobody move."`,
	}},
	{"drowned city", "world", []string{
		`*I climb off the ferry onto the rope bridge and look around.* "Where do I find something to eat around here?"`,
		`*I head for the Drowned Market.*`,
		`"Who's in charge here?"`,
		`*I buy a paper cone of fried eels and eat while I walk.*`,
		`*A bell starts ringing somewhere across the water, low and slow.* "What does that mean?"`,
		`*I follow the crowd.*`,
		`*I wait.*`,
		`"I need a boat that goes below the waterline tonight."`,
	}},
}

var odile = chars.Character{
	Name: "Odile Varga",
	Description: "Runs the Lamp and Anchor, a dockside bar in a port town the navy has half abandoned. Fifties, " +
		"an ex-smuggler who knows everyone's debts and keeps them to herself until they are useful. She wants " +
		"to sell the bar and leave before the harbour guild takes it from her, and she has told nobody.",
	Personality: "dry, watchful, unsentimental, quietly generous to people who do not ask",
	Appearance: "Short and heavy-shouldered, grey hair cropped close, a burn scar across the back of her left " +
		"hand. A man's waistcoat over rolled shirtsleeves. She wipes the same glass over and over while she listens.",
	Speech: "Short, flat sentences. Swears casually. Calls everyone \"love\" in a way that is not affectionate. " +
		"Answers a question with a price. Never says outright that she is scared.",
	Scenario: "The Lamp and Anchor, near closing on a wet night. {{user}} has come in out of the rain. The only " +
		"other customer is asleep in a corner booth.",
	FirstMes: "*The bell over the door gives its cracked little clank, and Odile does not look up from the glass " +
		"she is drying.* \"Kitchen's shut. Bar's shutting.\"\n\n*She sets the glass down at last and gives you the " +
		"long flat look she gives the tide tables.* \"You're letting the rain in, love.\"",
	MesExample: "<START>\n{{user}}: \"What do I owe you?\"\n{{char}}: *She doesn't check the slate.* \"More than " +
		"you've got. Sit down.\"",
}

var ysolde = chars.Character{
	Name: "Ysolde Brand",
	Description: "Captain of the border garrison's intelligence cell. Forties, patient, entirely without mercy. " +
		"Pain is a tool to her like any other, used without enjoyment and without regret. She has {{user}} " +
		"chained in a cellar under the customs house and needs the smuggling route before the next convoy.",
	Personality: "cold, methodical, soft-spoken, ruthless, never raises her voice",
	Appearance: "Tall and narrow, a dark uniform coat buttoned to the throat, gloves she takes off one finger at " +
		"a time before she touches anyone. Hair scraped back. A face that gives nothing away.",
	Speech: "Quiet and formal. Complete sentences, no contractions. Asks the same question several ways. " +
		"Threats are stated as plain facts, never shouted.",
	Scenario: "A cellar under the customs house, past midnight. {{user}} is chained to a chair. A brazier, a " +
		"table of instruments, one lamp.",
	FirstMes: "*The lamp hisses. Ysolde Brand draws off her gloves one finger at a time and lays them side by " +
		"side on the table, beside the instruments.*\n\n\"We will begin with your name, and then we will talk " +
		"about the north road.\" *She pulls a stool close and sits, her knee almost touching yours.* \"You may " +
		"make this as long as you like.\"",
}

func heistCast() []chars.Character {
	return []chars.Character{
		{Name: "Brannoc Teague", Personality: "calm, careful, controlling, never raises his voice",
			Description: "Planned the job and has planned twenty like it. Sixties. Believes in rehearsal and " +
				"nothing else. He is lying to the crew about who is paying for this.",
			Scenario: "A rented room above a bakery, the night before the job. {{user}} has brought the " +
				"building plans. Flour dust on everything, a single bulb."},
		{Name: "Suvi Lahti", Personality: "blunt, crude, impatient, fearless with locks and useless with people",
			Description: "The lockbreaker. Thirties, swears in every other sentence, cracks her knuckles when " +
				"she is thinking. She thinks Brannoc got her brother killed on the last job and has never said so."},
		{Name: "Ottavio Brisa", Personality: "nervous, talkative, vain about his work, loyal to whoever is kindest",
			Description: "The forger. Young, talks too much when he is frightened, and he is frightened now: " +
				"he owes money to the people who own the bank."},
	}
}

// saltgrave is the world scene's setting, with a lorebook whose keys the
// script says out loud.
func saltgrave(t *testing.T, st *store.Store) world.World {
	t.Helper()
	w := world.World{
		Name: "Saltgrave",
		Description: "A half-drowned port city built on the rooftops of the one beneath it. Oil lamps, rope " +
			"bridges, salt in everything. Roughly the 1800s.",
		Rules: "The Bell Guild controls the tides and sells high water to flood its rivals' districts.\n" +
			"Nobody goes below the waterline after dark.\n" +
			"Debts are recorded in the salt ledgers and inherited.",
	}
	id, err := st.SaveWorld(w)
	if err != nil {
		t.Fatal(err)
	}
	w.ID = id
	for _, e := range []world.Entry{
		{Name: "Bell Guild", Keys: []string{"bell guild", "guild", "bell", "ringers", "in charge"},
			Content: "The bell-ringers who control the tide gates. Grey coats, brass hand-bells at the belt. A slow low bell means a paid flood is coming to a district within the hour."},
		{Name: "Drowned Market", Keys: []string{"drowned market", "market", "eat", "food"},
			Content: "Barges lashed together in the old cathedral square. Fried eels, salt fish, stolen goods. Run by Mother Kesk, who takes a tenth of everything."},
		{Name: "Mother Kesk", Keys: []string{"kesk", "mother kesk"},
			Content: "An old woman with a ledger chained to her wrist. Owns the market barges and half the debts in the Low Wards. Owes the Bell Guild more than anyone knows."},
		{Name: "Low Wards", Keys: []string{"low wards", "waterline", "below", "boat"},
			Content: "The flooded streets under the rooftops. Boatmen called divers will take you down by lamplight for a price, never after dark, except Tobiah Grell, who will do anything for silver."},
	} {
		e.WorldID = id
		e.Enabled = true
		if _, err := st.SaveLoreEntry(e); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// Patterns for what a scene reply gets wrong.
var (
	quotedSpan = regexp.MustCompile(`"[^"\n]*"|\x{201c}[^\x{201d}\n]*\x{201d}`)
	// A sentence of narration whose subject is the person playing: deciding
	// what they do or feel.
	youLeads = regexp.MustCompile(`(?:^|[.!?*\n]\s*)You\b`)
	// Narration that has the person playing speak.
	youSpeak = regexp.MustCompile(`(?i)\byou (say|said|reply|replied|answer|answered|whisper|whispered|ask|asked|mutter|muttered|shout|shouted)\b`)
	// The scene brought to an end or skipped forward.
	sceneEnds = regexp.MustCompile(`(?i)\b(the end|to be continued|fade to black|hours later|the next morning|days later|later that night|time passes|end of scene)\b`)
	// Stepping out of the fiction to refuse, warn or comment.
	outOfFiction = regexp.MustCompile(`(?i)(i can't (continue|write|help)|i cannot (continue|write|help)|i won't (continue|write)|as an ai|i'm not able to|not comfortable (writing|continuing)|content warning|\(ooc|\booc:|\[note|trigger warning|i must (remind|stress)|let's keep (this|things))`)
	stockName    = regexp.MustCompile(`\b(Elara|Seraphina|Lyra|Kael|Aria|Thorne|Elias|Silas|Vance|Evelyn)\b`)
	nestedMark   = regexp.MustCompile(`\*"|"\*`)
	sisterWord   = regexp.MustCompile(`(?i)\bsister\b`)
)

// unmarkedChars is how much of a reply sits outside both asterisks and
// quotes, as chars.unmarkedProse counts it.
func unmarkedChars(s string) int {
	n := 0
	inStars, inQuotes := false, false
	for _, r := range s {
		switch {
		case r == '*':
			inStars = !inStars
			continue
		case r == '"' || r == '“' || r == '”':
			inQuotes = !inQuotes
			continue
		}
		if inStars || inQuotes || r == ' ' || r == '\t' || r == '\n' {
			continue
		}
		n++
	}
	return n
}

func paragraphs(s string) int {
	n := 0
	for _, p := range strings.Split(strings.TrimSpace(s), "\n\n") {
		if strings.TrimSpace(p) != "" {
			n++
		}
	}
	return n
}

type sceneTally struct {
	replies, words, unmarked, youLead, youSay, named, ends, outside, stock, nested, dashes int
	// leaks counts "sister", which no scene here has in it and the prompt's
	// own example of writing to the person playing does.
	leaks                 int
	shortParas, longParas int
	rep                   float64
	repN                  int
	// group only
	unlabelled, userLabel, allSpoke, speakers int
}

func (tl *sceneTally) line(name string) string {
	s := fmt.Sprintf("%s: %d replies, mean %d words, %d unmarked, %d narrate-you, %d you-say, %d named, "+
		"%d ends, %d out-of-fiction, %d stock names, %d nested marks, %d dashes, paragraphs <2: %d >4: %d, rep %.3f, %d sister",
		name, tl.replies, tl.words/max(1, tl.replies), tl.unmarked, tl.youLead, tl.youSay, tl.named, tl.ends,
		tl.outside, tl.stock, tl.nested, tl.dashes, tl.shortParas, tl.longParas, tl.rep/float64(max(1, tl.repN)), tl.leaks)
	if tl.speakers > 0 {
		s += fmt.Sprintf(", unlabelled beats %d, user label %d, everyone spoke %d, mean speakers %.1f",
			tl.unlabelled, tl.userLabel, tl.allSpoke, float64(tl.speakers)/float64(max(1, tl.replies)))
	}
	return s
}

func TestLiveScenesHeavy(t *testing.T) {
	client, model := designModel(t)
	st := liveStore(t)
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	cfg.PersonaName = livePlayer
	cfg.PersonaDescription = "A courier in a salt-stained coat who reads the messages they carry."
	rep := newReport(t, "scenes.txt")
	only := os.Getenv("ASTRAL_TEST_ONLY")
	controlPrompts(t)

	cast := heistCast()
	for i := range cast {
		id, err := st.SaveCharacter(cast[i])
		if err != nil {
			t.Fatal(err)
		}
		cast[i].ID = id
	}
	st.SetRelation(cast[0].ID, cast[1].ID, "Old partners. Suvi thinks Brannoc got her brother killed on their last job, and neither of them has ever said it out loud.")
	st.SetRelation(cast[1].ID, cast[2].ID, "Suvi thinks Ottavio is a liability and tells him so.")
	w := saltgrave(t, st)

	byKind := map[string]*sceneTally{}
	byScript := map[string]*sceneTally{}
	var order []string
	for r := 0; r < liveRuns(); r++ {
		for _, sc := range sceneScripts {
			if only != "" && !strings.Contains(sc.name, only) && sc.kind != only {
				continue
			}
			ch := store.Chat{Kind: store.KindRoleplay}
			var members []chars.Character
			var greeting string
			p := Persona(cfg)
			switch sc.kind {
			case "one":
				c := odile
				if sc.name == "cellar interrogation" {
					c = ysolde
				}
				members = []chars.Character{c}
				greeting = chars.Greeting(c, p)
			case "group":
				members = cast
				greeting = chars.Label(cast[0].Name, "*Brannoc doesn't look up from the window.* \"You're late. Put them on the table.\"")
			case "world":
				ch.WorldID = w.ID
				members = []chars.Character{Narrator(w)}
			}
			names := chars.CastNames(members)
			var hist []ollama.Message
			if greeting != "" {
				hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: greeting})
			}
			if byKind[sc.kind] == nil {
				byKind[sc.kind] = &sceneTally{}
			}
			if byScript[sc.name] == nil {
				byScript[sc.name] = &sceneTally{}
				order = append(order, sc.name)
			}
			tallies := []*sceneTally{byKind[sc.kind], byScript[sc.name]}
			rep.printf("\n\n######## %s (%s), run %d\n", sc.name, sc.kind, r+1)
			if greeting != "" {
				rep.printf("--- opening\n%s\n", greeting)
			}
			var replies []string
			for _, u := range sc.turns {
				hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: u})
				msgs := sceneArm(BuildFor(st, cfg, ch, members, hist))
				// The window's prefill, for a one-on-one scene whose replies
				// have stopped marking narration.
				prefilled := false
				if sc.kind != "group" && chars.NarrationDrifted(hist) {
					msgs = append(msgs, ollama.Message{Role: ollama.RoleAssistant, Content: chars.NarrationPrefill})
					prefilled = true
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				no := false
				msg, _, err := client.Chat(ctx, model, msgs, Options(cfg), &no, nil)
				cancel()
				if err != nil {
					t.Fatalf("%s: the model failed: %v", sc.name, err)
				}
				_, reply := ollama.SplitThinking(msg.Content)
				reply = strings.TrimSpace(reply)
				if prefilled {
					reply = chars.RestorePrefill(reply)
				}
				flags := sceneFlags(reply, replies, names, sc.kind == "group", tallies)
				if prefilled {
					flags = append(flags, "PREFILLED")
				}
				replies = append(replies, reply)
				rep.printf("\n--- user: %s\n--- reply (%dw, %dp) %v\n%s\n", u, words(reply), paragraphs(reply), flags, reply)

				stored := reply
				if sc.kind == "group" {
					// Stored as the window stores it: split into beats, an
					// unlabelled one given to the first of the cast, and the
					// labels put back on when the history is sent.
					var parts []string
					for _, b := range chars.SplitBeats(reply, names) {
						who := b.Name
						if who == "" {
							who = names[0]
						}
						parts = append(parts, chars.Label(who, b.Text))
					}
					stored = strings.Join(parts, "\n\n")
				}
				hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: stored})
			}
		}
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	rep.printf("\n\n")
	for _, name := range order {
		msg := byScript[name].line(name)
		rep.printf("%s\n", msg)
		t.Log(msg)
	}
	for _, k := range kinds {
		msg := byKind[k].line("ALL " + k)
		rep.printf("%s\n", msg)
		t.Log(msg)
	}
}

// sceneArms are experiments on parts of a scene's request that are not
// registered prompts, and so cannot be swapped with ASTRAL_TEST_CONTROL: each is
// a set of replacements made in every message of the request before it is
// sent. ASTRAL_TEST_ARM picks one.
var sceneArms = map[string][][2]string{
	// The example of writing to the person playing, as it was: it named a
	// relative, and a relative is also something a character can call
	// somebody. Ten bar replies in forty called the person playing "sister"
	// with it, none with the coat that replaced it.
	// Fewer speakers in a group, said in the closing block, where the turn
	// taking is argued every turn. Measured on the heist crew over three runs,
	// no effect: everyone spoke in 15 replies of 24 against 14 without it.
	"fewer": {
		{"Not everyone speaks. Choose whoever would actually react and leave the rest silent. ",
			"Not everyone speaks: one or two of them this turn, whoever would actually react, and the rest stay silent. "},
	},
	// Names for the people a world scene's narrator plays. Measured over three
	// runs of the drowned city, no effect: the narrator named nobody it
	// invented with it or without it, only the people in the lorebook.
	"names": {
		{"and play whoever they meet: give those people names, voices and reasons of their own, and let them leave again.",
			"and play whoever they meet. Anyone who speaks more than once has a name that fits the place, which comes out the way names do, when someone calls it or they give it, and a voice and a reason of their own; let them leave again."},
	},
	"sister": {
		{"your coat rather than her coat", "your sister rather than her sister"},
		{"your coat, not her coat", "your sister, not her sister"},
	},
}

func sceneArm(msgs []ollama.Message) []ollama.Message {
	arm, ok := sceneArms[os.Getenv("ASTRAL_TEST_ARM")]
	if !ok {
		return msgs
	}
	for i := range msgs {
		for _, r := range arm {
			msgs[i].Content = strings.ReplaceAll(msgs[i].Content, r[0], r[1])
		}
	}
	return msgs
}

// sceneFlags counts what is wrong with one scene reply into every tally given,
// and returns the flags to print beside it.
func sceneFlags(reply string, earlier, names []string, group bool, tallies []*sceneTally) []string {
	var flags []string
	narration := quotedSpan.ReplaceAllString(reply, " ")
	if group {
		// The labels are not narration.
		for _, n := range names {
			narration = strings.ReplaceAll(narration, n+":", " ")
		}
	}
	w := words(reply)
	un := unmarkedChars(reply)
	if group {
		for _, n := range names {
			un -= len(strings.ReplaceAll(n, " ", "")) + 1
		}
		un = max(0, un)
	}
	lead := len(youLeads.FindAllString(narration, -1))
	say := len(youSpeak.FindAllString(narration, -1))
	named := strings.Count(narration, livePlayer)
	ends := len(sceneEnds.FindAllString(reply, -1))
	out := len(outOfFiction.FindAllString(reply, -1))
	stock := len(stockName.FindAllString(reply, -1))
	nested := len(nestedMark.FindAllString(reply, -1))
	leak := len(sisterWord.FindAllString(reply, -1))
	d := dashes(reply)
	paras := paragraphs(reply)
	var rep float64
	scored := false
	if len(earlier) >= 2 {
		rep = chars.RepetitionScore(reply, earlier[max(0, len(earlier)-5):])
		scored = true
	}
	var unlabelled, speakers int
	userLabel, all := false, false
	if group {
		beats := chars.SplitBeats(reply, names)
		seen := map[string]int{}
		for _, b := range beats {
			if b.Name == "" {
				unlabelled++
				continue
			}
			seen[b.Name]++
		}
		speakers = len(seen)
		if len(seen) == len(names) {
			all = true
			for _, c := range seen {
				if c != 1 {
					all = false
				}
			}
		}
		userLabel = strings.HasPrefix(reply, livePlayer+":") || strings.Contains(reply, "\n"+livePlayer+":")
	}
	for _, tl := range tallies {
		tl.replies++
		tl.words += w
		if un >= 40 {
			tl.unmarked++
		}
		tl.youLead += lead
		tl.youSay += say
		tl.named += named
		tl.ends += ends
		tl.outside += out
		tl.stock += stock
		tl.nested += nested
		tl.leaks += leak
		tl.dashes += d
		if paras < 2 {
			tl.shortParas++
		}
		if paras > 4 {
			tl.longParas++
		}
		if scored {
			tl.rep += rep
			tl.repN++
		}
		if group {
			tl.unlabelled += unlabelled
			tl.speakers += speakers
			if userLabel {
				tl.userLabel++
			}
			if all {
				tl.allSpoke++
			}
		}
	}
	if un >= 40 {
		flags = append(flags, fmt.Sprintf("UNMARKED %d", un))
	}
	if lead > 0 {
		flags = append(flags, fmt.Sprintf("NARRATES YOU %d", lead))
	}
	if say > 0 {
		flags = append(flags, "YOU SAY")
	}
	if named > 0 {
		flags = append(flags, fmt.Sprintf("NAMED %d", named))
	}
	if ends > 0 {
		flags = append(flags, "ENDS OR SKIPS")
	}
	if out > 0 {
		flags = append(flags, "OUT OF FICTION")
	}
	if stock > 0 {
		flags = append(flags, "STOCK NAME")
	}
	if nested > 0 {
		flags = append(flags, "NESTED MARKS")
	}
	if d > 0 {
		flags = append(flags, fmt.Sprintf("%d DASHES", d))
	}
	if leak > 0 {
		flags = append(flags, "SISTER")
	}
	if scored {
		flags = append(flags, fmt.Sprintf("rep %.2f", rep))
	}
	if group {
		flags = append(flags, fmt.Sprintf("speakers %d", speakers))
		if unlabelled > 0 {
			flags = append(flags, fmt.Sprintf("UNLABELLED %d", unlabelled))
		}
		if userLabel {
			flags = append(flags, "SPEAKS AS USER")
		}
		if all {
			flags = append(flags, "ROLL CALL")
		}
	}
	return flags
}

// ----------------------------------------------------------- general chat ---

type chatCase struct {
	name string
	// turns are what the person says; each case is its own conversation.
	turns []string
	// maxWords is the longest a good answer to the last turn is, roughly.
	maxWords int
	// listOK says the question asks for things that are a list.
	listOK bool
}

var chatCases = []chatCase{
	{name: "capital", turns: []string{"What's the capital of Australia?"}, maxWords: 25},
	{name: "reverse list", turns: []string{"How do I reverse a list in Python?"}, maxWords: 80},
	{name: "heat pump", turns: []string{"How does a heat pump work?"}, maxWords: 260},
	{name: "tcp udp", turns: []string{"What's the difference between TCP and UDP?"}, maxWords: 260, listOK: true},
	{name: "villain names", turns: []string{"Give me five name ideas for a tabletop RPG villain who is a disgraced surgeon."}, maxWords: 150, listOK: true},
	{name: "go word count", turns: []string{"Write a Go function that reads a file line by line and counts the words in it."}, maxWords: 300},
	{name: "cookie dough", turns: []string{"Is it actually dangerous to eat raw cookie dough?"}, maxWords: 150},
	{name: "unknowable", turns: []string{"What was the exact population of Reykjavik on 1 March 2026?"}, maxWords: 90},
	{name: "gory fight", turns: []string{"Write a short, brutal, gory knife fight between two mercenaries in a stairwell. About 150 words, no holding back."}, maxWords: 260},
	{name: "explicit scene", turns: []string{"Write a short explicit sex scene between two consenting adults, a sailor and a dockside barkeep, about 150 words. Don't fade to black."}, maxWords: 260},
	{name: "lockpicking", turns: []string{"How does lock picking actually work on a pin tumbler lock? I want to understand the mechanism."}, maxWords: 260},
	{name: "feeling down", turns: []string{"I'm feeling kind of flat today and I don't really know why."}, maxWords: 120},
	{name: "thanks", turns: []string{"What's 15% of 80?", "Thanks!"}, maxWords: 20},
	{name: "wrong premise", turns: []string{"Since Python lists are immutable, what's the best way to add an item to one?"}, maxWords: 120},
}

var (
	fluffOpen    = regexp.MustCompile(`(?i)^\W*(great|good|excellent|interesting|fantastic) question|^\W*(sure|certainly|absolutely|of course|happy to help)\b`)
	offerClose   = regexp.MustCompile(`(?i)(let me know if|feel free to|hope (this|that) helps|happy to help|if you (have|need) any|would you like me to|want me to (go|expand|write|help))`)
	bulletLine   = regexp.MustCompile(`(?m)^\s*([-*\x{2022}]|\d+[.)])\s+\S`)
	boldLead     = regexp.MustCompile(`(?m)^\s*([-*\x{2022}]|\d+[.)])?\s*\*\*[^*\n]+\*\*\s*:?`)
	boldSpan     = regexp.MustCompile(`\*\*[^*\n]+\*\*`)
	mdHeading    = regexp.MustCompile(`(?m)^#{1,6} `)
	buzzword     = regexp.MustCompile(`(?i)\b(leverage|utili[sz]e|delve|robust|seamless|comprehensive|landscape|realm|journey|unlock|elevate|it is worth noting|it's worth noting)\b`)
	emoji        = regexp.MustCompile(`[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}]`)
	refusalWords = regexp.MustCompile(`(?i)(i can't (help|write|provide|assist)|i cannot (help|write|provide|assist)|i won't (write|provide)|i'm not able to|as an ai\b|not appropriate|i must (remind|emphasize|stress)|please (be careful|consult|seek|remember)|disclaimer|consult (a|your) (doctor|professional)|for educational purposes|legal (implications|consequences)|only (on|pick) locks you own|responsib(le|ly))`)
	codeFence    = regexp.MustCompile("(?m)^```([A-Za-z]*)")
)

func TestLiveGeneralChat(t *testing.T) {
	client, model := designModel(t)
	st := liveStore(t)
	cfg := store.DefaultConfig()
	// Off unless asked for, as for the designers; on is the default in the
	// window, and the searches are then answered by fakeSearch.
	cfg.WebSearch = os.Getenv("ASTRAL_TEST_SEARCH") != ""
	rep := newReport(t, "chat.txt")
	only := os.Getenv("ASTRAL_TEST_ONLY")
	controlPrompts(t)

	type tally struct {
		replies, long, fluff, offer, bullets, boldLeads, bold, headings, buzz, emoji, dashes, refusals int
		unfencedCode, bulletedUnasked, tails                                                           int
	}
	var tl tally
	for r := 0; r < liveRuns(); r++ {
		for _, cc := range chatCases {
			if only != "" && !strings.Contains(cc.name, only) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			ch := store.Chat{Kind: store.KindAssistant}
			var hist []ollama.Message
			rep.printf("\n\n######## %s, run %d\n", cc.name, r+1)
			for i, u := range cc.turns {
				hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: u})
				reply, _ := turn(ctx, t, client, model, st, cfg, ch, hist)
				hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: reply})
				if i < len(cc.turns)-1 {
					rep.printf("\n--- user: %s\n--- reply (%dw)\n%s\n", u, words(reply), reply)
					continue
				}
				// Code is not prose: its lines are not bullets and its
				// comments are not bold.
				prose := regexp.MustCompile("(?s)```.*?```").ReplaceAllString(reply, "")
				var flags []string
				tl.replies++
				if w := words(reply); w > cc.maxWords {
					tl.long++
					flags = append(flags, fmt.Sprintf("LONG %dw>%d", w, cc.maxWords))
				}
				if fluffOpen.MatchString(reply) {
					tl.fluff++
					flags = append(flags, "FLUFF OPENING")
				}
				if offerClose.MatchString(lastLines(reply, 2)) {
					tl.offer++
					flags = append(flags, "OFFERS MORE")
				}
				if t := tailLine(reply); t != "" {
					tl.tails++
					flags = append(flags, "TAIL: "+t)
				}
				b := len(bulletLine.FindAllString(prose, -1))
				tl.bullets += b
				if b > 0 && !cc.listOK {
					tl.bulletedUnasked++
					flags = append(flags, fmt.Sprintf("%d BULLETS", b))
				}
				if n := len(boldLead.FindAllString(prose, -1)); n > 0 {
					tl.boldLeads += n
					flags = append(flags, fmt.Sprintf("%d BOLD LEADS", n))
				}
				if n := len(boldSpan.FindAllString(prose, -1)); n > 1 {
					tl.bold += n
					flags = append(flags, fmt.Sprintf("%d BOLD", n))
				}
				if n := len(mdHeading.FindAllString(prose, -1)); n > 0 {
					tl.headings += n
					flags = append(flags, fmt.Sprintf("%d HEADINGS", n))
				}
				if n := len(buzzword.FindAllString(reply, -1)); n > 0 {
					tl.buzz += n
					flags = append(flags, fmt.Sprintf("BUZZ %v", buzzword.FindAllString(reply, -1)))
				}
				if n := len(emoji.FindAllString(reply, -1)); n > 0 {
					tl.emoji += n
					flags = append(flags, "EMOJI")
				}
				if n := dashes(reply); n > 0 {
					tl.dashes += n
					flags = append(flags, fmt.Sprintf("%d DASHES", n))
				}
				if m := refusalWords.FindAllString(reply, -1); len(m) > 0 {
					tl.refusals += len(m)
					flags = append(flags, fmt.Sprintf("HEDGE %v", m))
				}
				// Every other fence is an opening one, and each opening one
				// should name its language.
				for i, f := range codeFence.FindAllStringSubmatch(reply, -1) {
					if i%2 == 0 && f[1] == "" {
						tl.unfencedCode++
						flags = append(flags, "CODE WITHOUT LANGUAGE")
					}
				}
				rep.printf("\n--- user: %s\n--- reply (%dw) %v\n%s\n", u, words(reply), flags, reply)
			}
			cancel()
		}
	}
	msg := fmt.Sprintf("general chat: %d replies, %d over length, %d fluff openings, %d offers at the end, "+
		"%d bullet lines (%d replies bulleted unasked), %d bold lead-ins, %d bold (in replies with more than one), "+
		"%d headings, %d buzzwords, %d emoji, %d dashes, %d hedges or refusals, %d code blocks without a language, "+
		"%d tacked-on last lines",
		tl.replies, tl.long, tl.fluff, tl.offer, tl.bullets, tl.bulletedUnasked, tl.boldLeads, tl.bold,
		tl.headings, tl.buzz, tl.emoji, tl.dashes, tl.refusals, tl.unfencedCode, tl.tails)
	rep.printf("\n%s\n", msg)
	t.Log(msg)
}

// tailVerb opens the kind of last line that is not part of the answer: an
// instruction to go and check something, a suggestion, or a question about what
// they want next.
var tailVerb = regexp.MustCompile(`(?i)^(check|try|decide|identify|create|pick|consider|tell me|let me|feel free|look|make sure|test|run|use|you can|you could|you might|do you|would you|want|if you)\b`)

// tailLine is the last paragraph of a reply when it is one short sentence
// tacked on after the answer rather than part of it, and empty otherwise.
func tailLine(s string) string {
	s = regexp.MustCompile("(?s)```.*?```").ReplaceAllString(strings.TrimSpace(s), "")
	var paras []string
	for _, p := range strings.Split(s, "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			paras = append(paras, p)
		}
	}
	if len(paras) < 2 {
		return ""
	}
	last := paras[len(paras)-1]
	if words(last) > 25 || strings.Contains(last, "\n") {
		return ""
	}
	if tailVerb.MatchString(last) || strings.HasSuffix(last, "?") {
		return last
	}
	return ""
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// --------------------------------------------------------------- revisers ---

type reviseScript struct {
	name, kind string
	turns      []string
}

var reviseScripts = []reviseScript{
	{"weak card", store.KindDesigner, []string{
		"What do you think is weakest about her?",
		"Agreed. Make her a locksmith who lies about where she learned it. And colder, she shouldn't warm up to {{user}} easily.",
		"Good. That's enough, save it.",
	}},
	{"good card, one change", store.KindDesigner, []string{
		"She's fine, I just want her meaner. Crueller to people who owe her money.",
		"Yes, that. Nothing else changes.",
		"Save it.",
	}},
	{"default style", store.KindStyleDesigner, []string{
		"Everything comes out the same length, four paragraphs every time.",
		"One to two paragraphs. And the dialogue should be blunter.",
		"That's it, save it.",
	}},
	{"generic world", store.KindWorldDesigner, []string{
		"What's weakest about it?",
		"Make it harsher. The guild should drown a district every month whether anyone pays or not.",
		"Good, save it.",
	}},
}

var weakMara = chars.Character{
	Name:        "Mara",
	Description: "Mysterious. Complex. Interesting. A woman with a past.",
	Personality: "complicated",
	Scenario:    "You meet her.",
	FirstMes:    "\"Hello.\"",
}

func TestLiveRevisersHeavy(t *testing.T) {
	client, model := designModel(t)
	st := liveStore(t)
	cfg := store.DefaultConfig()
	cfg.WebSearch = false
	cfg.PersonaName = livePlayer
	rep := newReport(t, "revisers.txt")
	only := os.Getenv("ASTRAL_TEST_ONLY")
	controlPrompts(t)
	w := saltgrave(t, st)

	type tally struct{ replies, long, manyQ, lists, hedges, button, dashes, praise int }
	byKind := map[string]*tally{}
	revised := map[string]*reviseResult{}
	for r := 0; r < liveRuns(); r++ {
		for _, sc := range reviseScripts {
			if only != "" && !strings.Contains(sc.name, only) && sc.kind != only {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			ch := store.Chat{Kind: sc.kind}
			var ca chars.Character
			var opening, button string
			switch sc.name {
			case "weak card":
				ca = weakMara
				opening, button = chars.ReviseOpening(ca), "save character"
			case "good card, one change":
				ca = odile
				opening, button = chars.ReviseOpening(ca), "save character"
			case "default style":
				ch.Note = "Default Copy"
				cfg.WritingStyles = []chars.WritingStyle{{Name: "Default Copy", Instructions: chars.DefaultStyle().Instructions}}
				opening, button = chars.ReviseStyleOpening(cfg.WritingStyles[0]), "save style"
			case "generic world":
				ch.WorldID = w.ID
				opening, button = world.ReviseOpening(w), "save world"
			}
			tl := byKind[sc.kind]
			if tl == nil {
				tl = &tally{}
				byKind[sc.kind] = tl
			}
			hist := []ollama.Message{{Role: ollama.RoleAssistant, Content: opening}}
			rep.printf("\n\n######## %s (%s), run %d\n", sc.name, sc.kind, r+1)
			for i, u := range sc.turns {
				hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: u})
				msgs := Build(st, cfg, ch, ca, hist)
				think := false
				rn := Runner(client, cfg, st, ch.Kind, model, OptionsFor(cfg, ch.Kind), &think)
				msg, _, _, err := rn.Run(ctx, msgs, nil)
				if err != nil {
					t.Fatalf("the model failed: %v", err)
				}
				_, reply := ollama.SplitThinking(msg.Content)
				reply = strings.TrimSpace(reply)
				hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: reply})
				low := strings.ToLower(reply)
				var flags []string
				tl.replies++
				if n := words(reply); n > 170 {
					tl.long++
					flags = append(flags, fmt.Sprintf("LONG %dw", n))
				}
				if q := strings.Count(reply, "?"); q > 2 {
					tl.manyQ++
					flags = append(flags, fmt.Sprintf("%d QUESTIONS", q))
				}
				if n := len(bulletLine.FindAllString(reply, -1)); n > 0 {
					tl.lists++
					flags = append(flags, fmt.Sprintf("%d LIST LINES", n))
				}
				for _, h := range hedgeWords {
					if strings.Contains(low, h) {
						tl.hedges++
						flags = append(flags, "HEDGE "+h)
					}
				}
				if n := dashes(reply); n > 0 {
					tl.dashes += n
					flags = append(flags, fmt.Sprintf("%d DASHES", n))
				}
				if i == 0 && regexp.MustCompile(`(?i)\b(strong|great|excellent|love|works well|solid)\b`).MatchString(reply) {
					tl.praise++
					flags = append(flags, "PRAISES")
				}
				if i == len(sc.turns)-1 {
					if strings.Contains(low, button) {
						tl.button++
					} else {
						flags = append(flags, "NO BUTTON NAMED")
					}
				}
				rep.printf("\n--- user: %s\n--- reply (%dw) %v\n%s\n", u, words(reply), flags, reply)
			}

			// And what pressing the button would save, built two ways: as the
			// window builds a revision, with the designer's own build step,
			// which is handed the conversation and not the card, and with the
			// revision build, which is handed the card as it stands.
			opts := Options(cfg)
			for _, how := range []string{"conversation only", "with the card"} {
				rv := revised[how]
				if rv == nil {
					rv = &reviseResult{}
					revised[how] = rv
				}
				fresh := how == "conversation only"
				switch sc.kind {
				case store.KindDesigner:
					var c chars.Character
					var err error
					if fresh {
						c, err = chars.BuildFromConversation(ctx, client, model, hist, opts)
					} else {
						c, err = chars.ReviseFromConversation(ctx, client, model, ca, hist, opts)
					}
					if err != nil {
						t.Errorf("%s: building failed: %v", sc.name, err)
						continue
					}
					got := chars.Revise(ca, c)
					rep.printf("\n=== SAVED CHARACTER, %s\nname: %s\ndescription: %s\npersonality: %s\nappearance: %s\nspeech: %s\nscenario: %s\nfirst_mes: %s\n",
						how, got.Name, got.Description, got.Personality, got.Appearance, got.Speech, got.Scenario, got.FirstMes)
					var kept []float64
					landed := false
					all := strings.ToLower(got.Description + " " + got.Personality + " " + got.Speech)
					if sc.name == "weak card" {
						// Everything on this card was meant to change except
						// her name, so only the name is kept or lost.
						landed = strings.Contains(all, "locksmith")
					} else {
						kept = []float64{overlap(ca.Appearance, got.Appearance), overlap(ca.Scenario, got.Scenario),
							overlap(ca.FirstMes, got.FirstMes), overlap(ca.Description, got.Description)}
						landed = !strings.Contains(strings.ToLower(got.Personality), "generous") &&
							regexp.MustCompile(`cruel|mean|debt|owe|contempt|vicious|ruthless|humiliat`).MatchString(all)
					}
					rv.add(got.Name == ca.Name, kept, landed)
					rep.printf("kept name %v, kept appearance/scenario/opening/description words %v, change landed %v\n",
						got.Name == ca.Name, pct(kept), landed)
				case store.KindStyleDesigner:
					var st chars.WritingStyle
					var err error
					if fresh {
						st, err = chars.BuildStyleFromConversation(ctx, client, model, hist, opts)
					} else {
						st, err = chars.ReviseStyleFromConversation(ctx, client, model, cfg.WritingStyles[0], hist, opts)
					}
					if err != nil {
						t.Errorf("%s: building failed: %v", sc.name, err)
						continue
					}
					rep.printf("\n=== SAVED STYLE, %s\nname: %s\n%s\n", how, st.Name, st.Instructions)
					was := styleLines(chars.DefaultStyle().Instructions)
					now := styleLines(st.Instructions)
					kept := []float64{overlap(was["Sentences"], now["Sentences"]), overlap(was["Description"], now["Description"]),
						overlap(was["Avoid"], now["Avoid"])}
					landed := regexp.MustCompile(`(?i)(one|1) (to|or) (two|2)`).MatchString(now["Length"]) &&
						regexp.MustCompile(`(?i)blunt|direct|clipped`).MatchString(now["Dialogue"])
					rv.add(st.Name == cfg.WritingStyles[0].Name, kept, landed)
					rep.printf("kept name %v, kept sentences/description/avoid words %v, change landed %v\n",
						st.Name == cfg.WritingStyles[0].Name, pct(kept), landed)
				case store.KindWorldDesigner:
					var d world.Draft
					var err error
					if fresh {
						d, err = world.BuildFromConversation(ctx, client, model, hist, opts)
					} else {
						d, err = world.ReviseFromConversation(ctx, client, model, w, hist, opts)
					}
					if err != nil {
						t.Errorf("%s: building failed: %v", sc.name, err)
						continue
					}
					got := world.Revise(w, d.World)
					rep.printf("\n=== SAVED WORLD, %s\nname: %s\ndescription: %s\nrules: %s\n", how, got.Name, got.Description, got.Rules)
					kept := []float64{overlap(w.Description, got.Description), overlap(
						"Nobody goes below the waterline after dark. Debts are recorded in the salt ledgers and inherited.", got.Rules)}
					landed := regexp.MustCompile(`(?i)month`).MatchString(got.Rules)
					rv.add(got.Name == w.Name, kept, landed)
					rep.printf("kept name %v, kept description/untouched rules words %v, change landed %v\n",
						got.Name == w.Name, pct(kept), landed)
				}
			}
			cancel()
		}
	}
	for kind, tl := range byKind {
		msg := fmt.Sprintf("%s revise: %d replies, %d long, %d with >2 questions, %d with list lines, %d hedges, %d dashes, %d first replies praising, button named %d times",
			kind, tl.replies, tl.long, tl.manyQ, tl.lists, tl.hedges, tl.dashes, tl.praise, tl.button)
		rep.printf("\n%s", msg)
		t.Log(msg)
	}
	for _, how := range []string{"conversation only", "with the card"} {
		if rv := revised[how]; rv != nil {
			msg := fmt.Sprintf("saved, built from the %s: %d builds, name kept %d, mean share of untouched words kept %.0f%%, change landed %d",
				how, rv.builds, rv.names, 100*rv.kept/float64(max(1, rv.keptN)), rv.landed)
			rep.printf("\n%s", msg)
			t.Log(msg)
		}
	}
}

// reviseResult is how well revisions built one way kept what they were not
// asked to change, and made the change they were.
type reviseResult struct {
	builds, names, landed, keptN int
	kept                         float64
}

func (r *reviseResult) add(name bool, kept []float64, landed bool) {
	r.builds++
	if name {
		r.names++
	}
	if landed {
		r.landed++
	}
	for _, k := range kept {
		r.kept += k
		r.keptN++
	}
}

func pct(xs []float64) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = fmt.Sprintf("%.0f%%", 100*x)
	}
	return out
}

// styleLines splits an assembled style into its labelled lines.
func styleLines(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// overlap is the share of the distinct words of a that are also in b.
func overlap(a, b string) float64 {
	norm := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, w := range strings.Fields(strings.ToLower(s)) {
			w = strings.Trim(w, ".,;:!?\"'*()")
			if len(w) > 3 {
				m[w] = true
			}
		}
		return m
	}
	wa, wb := norm(a), norm(b)
	if len(wa) == 0 {
		return 1
	}
	n := 0
	for w := range wa {
		if wb[w] {
			n++
		}
	}
	return float64(n) / float64(len(wa))
}

// ------------------------------------------------------------ web search ---

// fakeSearch answers searches from memory, so the guidance can be exercised
// as it is sent without anything leaving the machine.
type fakeSearch struct {
	fail    bool
	queries []string
}

func (f *fakeSearch) Name() string { return "Test Search" }

func (f *fakeSearch) Search(ctx context.Context, q string, n int) ([]websearch.Result, error) {
	f.queries = append(f.queries, q)
	if f.fail {
		return nil, errors.New("dial tcp: connection refused")
	}
	return []websearch.Result{
		{Title: "Go 1.27 Release Notes, The Go Programming Language", URL: "https://go.dev/doc/go1.27",
			Snippet: "Go 1.27, released 11 August 2026, is the latest version of Go. It adds iterator adapters to the standard library and..."},
		{Title: "Release History, The Go Programming Language", URL: "https://go.dev/doc/devel/release",
			Snippet: "go1.27.1 (released 2026-09-02) includes security fixes to the net/http package..."},
		{Title: "Top 10 reasons to learn Go in 2026", URL: "https://example-listicle.com/go-2026",
			Snippet: "Go 1.25 is the newest version and it is amazing..."},
	}, nil
}

type searchCase struct {
	name, kind string
	turns      []string
	// should says whether the last turn ought to search.
	should bool
	fail   bool
}

var searchCases = []searchCase{
	{name: "go version", kind: store.KindAssistant, turns: []string{"What's the newest released version of Go?"}, should: true},
	{name: "python version", kind: store.KindAssistant, turns: []string{"Which version of Python should I install today? I want the latest stable one."}, should: true},
	{name: "prime minister", kind: store.KindAssistant, turns: []string{"Who is the prime minister of the UK right now?"}, should: true},
	{name: "arithmetic", kind: store.KindAssistant, turns: []string{"What is 17 times 23?"}},
	{name: "closure", kind: store.KindAssistant, turns: []string{"Explain what a closure is in JavaScript, briefly."}},
	{name: "haiku", kind: store.KindAssistant, turns: []string{"Write me a haiku about rain on a tin roof."}},
	{name: "go version, search down", kind: store.KindAssistant, turns: []string{"What's the newest released version of Go?"}, should: true, fail: true},
	{name: "designer detective", kind: store.KindDesigner, turns: []string{
		"A tired detective in 1950s Los Angeles.",
		"She drinks, she lies to her captain, she lost her partner. That's enough, build her.",
	}},
	{name: "world designer", kind: store.KindWorldDesigner, turns: []string{
		"A drowned coastal city where the tides are controlled by a guild of bell-ringers.",
		"Technology is roughly 1800s. That's enough, build it.",
	}},
}

func TestLiveSearchGuidance(t *testing.T) {
	client, model := designModel(t)
	st := liveStore(t)
	cfg := store.DefaultConfig()
	cfg.WebSearch = true
	rep := newReport(t, "search.txt")
	only := os.Getenv("ASTRAL_TEST_ONLY")
	controlPrompts(t)

	var right, wrong, sentence, sourced, admitted, saved, button, designerTurns, designerDashes, designerManyQ int
	for r := 0; r < liveRuns(); r++ {
		for _, sc := range searchCases {
			if only != "" && !strings.Contains(sc.name, only) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			ch := store.Chat{Kind: sc.kind}
			var hist []ollama.Message
			if sc.kind != store.KindAssistant {
				hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: openingFor(sc.kind)})
			}
			rep.printf("\n\n######## %s (%s), run %d\n", sc.name, sc.kind, r+1)
			for i, u := range sc.turns {
				hist = append(hist, ollama.Message{Role: ollama.RoleUser, Content: u})
				msgs := Build(st, cfg, ch, chars.Character{}, hist)
				think := false
				rn := Runner(client, cfg, st, ch.Kind, model, OptionsFor(cfg, ch.Kind), &think)
				fake := &fakeSearch{fail: sc.fail}
				rn.Provider = fake
				rn.Fetcher = nil
				msg, _, rounds, err := rn.Run(ctx, msgs, nil)
				if err != nil {
					t.Fatalf("%s: %v", sc.name, err)
				}
				_, reply := ollama.SplitThinking(msg.Content)
				reply = strings.TrimSpace(reply)
				hist = append(hist, ollama.Message{Role: ollama.RoleAssistant, Content: reply})
				var flags []string
				for _, rd := range rounds {
					switch {
					case rd.Note != "":
						flags = append(flags, "TOOL: "+rd.Note)
						if strings.HasPrefix(rd.Note, "Saved") {
							saved++
						}
					case rd.Opened != "":
						flags = append(flags, "OPENED "+rd.Opened)
					default:
						flags = append(flags, fmt.Sprintf("SEARCHED %q", rd.Query))
					}
				}
				if sc.kind != store.KindAssistant {
					designerTurns++
					designerDashes += dashes(reply)
					if strings.Count(reply, "?") > 2 {
						designerManyQ++
					}
				}
				if i == len(sc.turns)-1 {
					searched := len(fake.queries) > 0
					switch {
					case sc.kind != store.KindAssistant:
						if searched {
							wrong++
						} else {
							right++
						}
						low := strings.ToLower(reply)
						if strings.Contains(low, "create character") || strings.Contains(low, "create world") {
							button++
						} else {
							flags = append(flags, "NO BUTTON NAMED")
						}
					case searched == sc.should:
						right++
					default:
						wrong++
						flags = append(flags, "WRONG SEARCH DECISION")
					}
					for _, q := range fake.queries {
						if strings.HasSuffix(strings.TrimSpace(q), "?") || len(strings.Fields(q)) > 8 {
							sentence++
							flags = append(flags, "QUERY IS A SENTENCE")
						}
					}
					if sc.should && !sc.fail && searched && strings.Contains(reply, "go.dev") {
						sourced++
					}
					if sc.fail && searched && regexp.MustCompile(`(?i)(could not|couldn't|unable|failed|not able|can't check|cannot check|wasn't able|search (was|is) (down|unavailable))`).MatchString(reply) {
						admitted++
					}
				}
				rep.printf("\n--- user: %s\n--- reply (%dw) %v\n%s\n", u, words(reply), flags, reply)
			}
			cancel()
		}
	}
	msg := fmt.Sprintf("search: %d right decisions, %d wrong, %d sentence queries, %d answers naming go.dev, "+
		"%d admitted a failed search, %d saves to knowledge, designers: %d turns, %d dashes, %d with >2 questions, button named %d",
		right, wrong, sentence, sourced, admitted, saved, designerTurns, designerDashes, designerManyQ, button)
	rep.printf("\n%s\n", msg)
	t.Log(msg)
}

// ---------------------------------------------------------------- pictures ---

// TestLivePictures reads one picture, ASTRAL_TEST_IMAGE, with both framings,
// as Describe sends it, and counts what the prompts forbid: a preamble, an
// offer at the end, refusing or softening, and in a design chat a missing
// "Reads as:" paragraph. The model is sent the picture directly rather than
// through Describe, which may set aside whatever is loaded to make room.
func TestLivePictures(t *testing.T) {
	client, model := designModel(t)
	path := os.Getenv("ASTRAL_TEST_IMAGE")
	if path == "" {
		t.Skip("set ASTRAL_TEST_IMAGE to a picture to read")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	image64 := base64.StdEncoding.EncodeToString(raw)
	rep := newReport(t, "pictures.txt")
	controlPrompts(t)
	preamble := regexp.MustCompile(`(?i)^\W*(here|this image|the image|sure|certainly|okay|of course|in this image)\b`)
	var replies, pre, offers, refusals, readsAs, designs, words0 int
	for r := 0; r < liveRuns(); r++ {
		for _, design := range []bool{false, true} {
			for _, said := range []string{"", "What would the weather be like here?"} {
				msgs := []ollama.Message{
					{Role: ollama.RoleSystem, Content: chars.SeeingPrompt(design)},
					{Role: ollama.RoleUser, Content: chars.SeeingRequest(said), Images: []string{image64}},
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				no := false
				msg, _, err := client.Chat(ctx, model, msgs, seeingOptions, &no, nil)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				_, text := ollama.SplitThinking(msg.Content)
				text = strings.TrimSpace(text)
				var flags []string
				replies++
				words0 += words(text)
				if preamble.MatchString(text) {
					pre++
					flags = append(flags, "PREAMBLE")
				}
				if offerClose.MatchString(lastLines(text, 2)) {
					offers++
					flags = append(flags, "OFFERS MORE")
				}
				if m := refusalWords.FindAllString(text, -1); len(m) > 0 {
					refusals += len(m)
					flags = append(flags, fmt.Sprintf("HEDGE %v", m))
				}
				if design {
					designs++
					if strings.Contains(strings.ToLower(text), "reads as:") {
						readsAs++
					} else {
						flags = append(flags, "NO READS AS")
					}
				}
				rep.printf("\n\n######## design=%v said=%q run %d\n--- reply (%dw) %v\n%s\n", design, said, r+1, words(text), flags, text)
			}
		}
	}
	msg := fmt.Sprintf("pictures: %d readings, mean %d words, %d preambles, %d offers at the end, %d hedges, \"Reads as:\" in %d of %d design readings",
		replies, words0/max(1, replies), pre, offers, refusals, readsAs, designs)
	rep.printf("\n%s\n", msg)
	t.Log(msg)
}
