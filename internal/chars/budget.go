package chars

// Astral measures its prompts in characters, because counting real tokens
// would mean shipping a tokenizer per model. That is a reasonable trade, but
// it only works if every budget is derived from the same place. They were not:
// the transcript cap, the lore cap and the recap cap were three independent
// constants, chosen separately and never added up against the context window
// they all had to share.
//
// Added up, they did not fit. A long scene with a rich card, a full lorebook
// and a recap built a prompt of about 8,500 tokens against a default window of
// 8,192, with nothing left over for the reply. What happens then is quiet and
// bad: the server slides the window forward and drops the *oldest* tokens,
// which are the system prompt. The scene keeps its small talk and loses the
// framing that says to use asterisks, the writing style, and the character
// description. It does not fail; it just gradually stops following the rules,
// which is exactly what it looked like from the outside.
//
// So the window is divided once, here, and everything else asks.

// charsPerToken converts a token budget into a character budget.
//
// The usual rule of thumb is four. This uses three and a half, deliberately:
// the ratio is worse than four for the things roleplay prose is full of —
// names, contractions, asterisks, quotation marks — and the cost of guessing
// low is a slightly shorter transcript, while the cost of guessing high is the
// silent truncation described above.
const charsPerToken = 3.5

// DefaultReplyTokens is how much room a reply is assumed to need when no
// explicit limit is set. Four paragraphs of roleplay prose runs to about six
// hundred tokens; this leaves headroom over that.
const DefaultReplyTokens = 1024

// safetyTokens is held back for what this arithmetic cannot see: the chat
// template's own wrapping, per-message role markers, and the difference
// between a token estimate and a tokenizer.
const safetyTokens = 256

// blockFramingChars is the fixed text wrapped around the recap and the lore:
// the sentences saying what each block is, that it is notes rather than prose,
// and how to treat it. Around two hundred characters each.
//
// It is not part of either block's budget, and it is in every prompt that
// carries them, so it comes off the top. Lengthening that wording without this
// is how the worst case went seventeen tokens over an 8k window, which
// TestWorstCaseNowFits caught.
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
	// Overflows records that the fixed parts did not fit on their own. The
	// prompt is still sent — a character with a very long description in a
	// very small window is a real thing to want — but it will be truncated by
	// the server, and the user is better told than left to wonder why the
	// scene stopped following its own rules.
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

// Plan divides a context window between the parts of a prompt.
//
// numCtx is the window in tokens, numPredict the reply limit (zero means the
// default), and fixedChars is the size of everything that is not negotiable:
// the system prompt with the character, the style and the instructions in it.
// That is measured rather than estimated, because a rich card and a bare one
// differ by thousands of characters and the difference has to come out of
// somewhere.
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
	// The system prompt is not negotiable, so it is served first and
	// everything else divides what is left. Sizing lore and recap against the
	// whole window instead reads better — a long character card has nothing
	// to do with how big a lorebook should be — but it does not add up: a
	// 9,000-character card in a 4,096-token window then plans a prompt larger
	// than the window, which is the exact bug this file exists to stop.
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
