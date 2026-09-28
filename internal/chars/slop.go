package chars

import (
	"regexp"
	"strings"
	"sync"

	"astral/internal/prompts"
)

// Stock phrases, the ones roleplayers call slop: "a shiver ran down her spine",
// "barely above a whisper", "a mischievous glint". Every model trained on the
// same fiction reaches for them, and a reader who has seen one a hundred times
// stops reading the sentence around it.
//
// Asking a model not to write them is known not to work well, and naming them
// in the prompt risks planting them. So the reply is watched as it streams,
// and when one appears the reply is cut back to just before it and the model
// carries on from there, told only about the phrase it just used. See
// scene.Unslop. Ollama cannot ban a phrase itself; this is the app doing what
// KoboldCpp's and TabbyAPI's phrase bans do.

// stockPhrases is the list, one phrase per line. A * stands for any one word.
// Phrases rather than single words, apart from a few words that are stock in
// any use: a word ban makes prose evasive, a phrase ban only makes it stop
// reaching for the same thing.
const stockPhrases = `shiver down * spine
shivers down * spine
shiver * down * spine
shivers * down * spine
shiver up * spine
shiver * up * spine
sent a shiver
sends a shiver
sending a shiver
sent shivers
sends shivers
barely above a whisper
mischievous glint
glint in * eye
glint in * eyes
eyes glinting with mischief
eyes sparkling with mischief
smirk
smirks
smirked
smirking
breath hitches
breath hitched
breath hitching
ministrations
a mix of * and
a mixture of * and
testament to
pregnant pause
smell of ozone
scent of ozone
for what felt like an eternity
for what feels like an eternity
couldn't help but
maybe, just maybe
i don't bite
voice dripping with
voice laced with
eyes darken with
eyes darkened with
wicked grin
low chuckle
knowing smile
the air was thick with
the air is thick with
electricity crackled
unspoken promise
a promise of more`

var promptStock = prompts.Register(prompts.Prompt{
	ID: "scene.stock-phrases", Name: "Stock Phrases", Group: "Scenes",
	About: "Not sent to the model. Phrases a scene's reply is never allowed to keep: when one is " +
		"written, the reply is cut back to just before it and carried on. One per line; a * stands " +
		"for any one word.",
	Default: stockPhrases,
	List:    true,
})

var (
	stockMu    sync.Mutex
	stockText  string
	stockCache []StockPhrase
	stockLen   int
)

// StockLen is the most characters a phrase on the list can take, a * counted
// as a long word. It is how much of a reply is held back while it streams:
// the shorter it is, the sooner the first words show.
func StockLen() int {
	StockPhrases()
	stockMu.Lock()
	defer stockMu.Unlock()
	return stockLen
}

// StockPhrase is one phrase and the pattern that finds it.
type StockPhrase struct {
	Phrase string
	re     *regexp.Regexp
}

// StockPhrases is the current list, compiled. Rebuilt only when the list in
// Prompts changes.
func StockPhrases() []StockPhrase {
	text := prompts.Text(promptStock)
	stockMu.Lock()
	defer stockMu.Unlock()
	if text == stockText && stockCache != nil {
		return stockCache
	}
	stockText = text
	stockCache = nil
	stockLen = 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		stockLen = max(stockLen, len(line)+strings.Count(line, "*")*(stockWord-1))
		var parts []string
		for _, w := range strings.Fields(line) {
			if w == "*" {
				parts = append(parts, `[\p{L}'’-]+`)
				continue
			}
			parts = append(parts, regexp.QuoteMeta(strings.ReplaceAll(w, "'", "’")))
		}
		// Straight and curly apostrophes alike: the text is matched with its
		// apostrophes curled, so a straight one in the list still finds both.
		pattern := `(?i)(^|[^\p{L}])(` + strings.Join(parts, `\s+`) + `)($|[^\p{L}])`
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		stockCache = append(stockCache, StockPhrase{Phrase: line, re: re})
	}
	return stockCache
}

// stockWord is how long a word a * is allowed to stand for, in the reckoning
// of how much to hold back. A phrase whose * words run longer can have its
// start shown before it is complete, and is then left to stand: a rare miss,
// against every reply's first words waiting on the longest word there is.
const stockWord = 14

// MaxStockChars is the longest a stock phrase is likely to run, which is how
// much of a reply is held back while it streams so a phrase is caught before
// it is ever shown.
const MaxStockChars = 64

// FindStock reports the first stock phrase in s that is not allowed, where it
// starts and where it ends. -1 when there is none.
func FindStock(s string, allowed map[string]bool) (string, int, int) {
	// Curled so one pattern matches both apostrophes. The same length in
	// bytes is not guaranteed, so positions are mapped back below.
	curled := strings.ReplaceAll(s, "'", "’")
	best, at, end := "", -1, -1
	for _, p := range StockPhrases() {
		if allowed[p.Phrase] {
			continue
		}
		loc := p.re.FindStringSubmatchIndex(curled)
		if loc == nil {
			continue
		}
		// The phrase itself, not the boundaries either side of it.
		if at < 0 || loc[4] < at {
			best, at, end = p.Phrase, loc[4], loc[5]
		}
	}
	if at < 0 {
		return "", -1, -1
	}
	return best, straightIndex(s, at), straightIndex(s, end)
}

// straightIndex maps a byte offset in s with its apostrophes curled back to
// the same point in s itself. A curly apostrophe is three bytes and a straight
// one is one, so every straight apostrophe before the point moves it by two.
func straightIndex(s string, curledAt int) int {
	pos, curled := 0, 0
	for pos < len(s) && curled < curledAt {
		if s[pos] == '\'' {
			curled += len("’")
		} else {
			curled++
		}
		pos++
	}
	return pos
}
