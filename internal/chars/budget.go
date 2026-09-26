package chars

// Prompts are measured in characters, not tokens: a tokenizer per model is
// not worth shipping.
//
// Every budget is derived here and nowhere else. A prompt that overruns the
// window is not an error: the server drops the oldest tokens, which are the
// system prompt, so the scene keeps its small talk and silently loses its
// framing.

// charsPerToken is 3.5 rather than the usual 4: roleplay prose tokenizes
// worse, and guessing low costs a shorter transcript while guessing high costs
// the truncation above.
const charsPerToken = 3.5

// DefaultReplyTokens is the room reserved for a reply when no limit is set.
// Four paragraphs runs to about six hundred tokens.
const DefaultReplyTokens = 1024

// safetyTokens covers what this arithmetic cannot see: the chat template's
// wrapping, role markers, and estimate against tokenizer.
const safetyTokens = 256

// blockFramingChars is the fixed wording around the recap and lore blocks,
// which is in every prompt carrying them and so comes off the top. Lengthening
// that wording without raising this overruns the window; TestWorstCaseNowFits
// holds it.
const blockFramingChars = 460

// Budget is how many characters each part of a prompt may spend.
type Budget struct {
	// History is the verbatim transcript, and takes whatever is left after
	// the fixed parts are served.
	History int
	// Lore and Recap are capped as a share of the window, because both grow
	// on their own and neither should be able to crowd out the scene.
	Lore  int
	Recap int
	// Compact is the transcript size at which a scene is folded into the
	// recap, and Keep is how much stays verbatim afterwards. They are derived
	// so that compaction always triggers before History is exceeded.
	Compact int
	Keep    int
	// Overflows records that the fixed parts did not fit alone. The prompt is
	// still sent and will be truncated by the server; the user is told rather
	// than left wondering why the scene stopped following its rules.
	Overflows bool
}

// Share of the usable window each capped part may take. The remainder is the
// transcript, which is the part worth spending on.
const (
	loreShare  = 0.16
	recapShare = 0.12
)

// Floors, so a small context window degrades to something still playable
// rather than to nothing. Below these the scene is not worth the lorebook's
// space at all, and Plan says so by returning zero.
const (
	minLoreChars  = 600
	minRecapChars = 500
)

// Plan divides a context window between the parts of a prompt. fixedChars is
// the measured size of the system prompt, which a rich card and a bare one
// differ on by thousands of characters.
func Plan(numCtx, numPredict, fixedChars int) Budget {
	reply := numPredict
	if reply <= 0 {
		reply = DefaultReplyTokens
	}
	usable := numCtx - reply - safetyTokens
	if usable < 0 {
		usable = 0
	}
	total := int(float64(usable)*charsPerToken) - blockFramingChars
	if total < 0 {
		total = 0
	}

	b := Budget{}
	// Served first, because sizing lore and recap against the whole window
	// instead lets a 9,000-character card in a 4k window plan a prompt larger
	// than the window.
	remaining := total - fixedChars
	if remaining < 0 {
		remaining = 0
		b.Overflows = true
	}

	// Each capped part takes the smaller of its share of the window and a
	// minority of what is actually left, so the transcript stays the majority
	// of the prompt however little room there is.
	take := func(share float64, floor int, pool int) int {
		n := int(float64(total) * share)
		if cap := pool * 2 / 5; n > cap {
			n = cap
		}
		if n < floor {
			return 0
		}
		return n
	}
	b.Lore = take(loreShare, minLoreChars, remaining)
	b.Recap = take(recapShare, minRecapChars, remaining-b.Lore)

	b.History = remaining - b.Lore - b.Recap
	if b.History < 0 {
		b.History = 0
	}

	// Compaction has to happen before the transcript hits its cap, or trimming
	// silently throws turns away that the recap never got a chance to read.
	// Three quarters leaves a comfortable margin, and the quarter between the
	// two is what one pass reclaims — wide enough that it does not run again
	// on the very next turn.
	b.Compact = b.History * 3 / 4
	b.Keep = b.Compact / 2
	return b
}

// DefaultBudget is the plan for Astral's default settings. It exists so code
// and tests that have no configuration to hand still divide the same window
// the same way, rather than inventing a constant of their own.
func DefaultBudget() Budget { return Plan(8192, 0, 4000) }
