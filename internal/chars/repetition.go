package chars

import (
	"sort"
	"strings"
	"unicode"

	"astral/internal/ollama"
)

// A model in a long scene settles into habits. The same gesture comes back
// every third reply, the same simile, the same way of opening a paragraph, and
// because its own transcript is the strongest instruction in its context each
// repeat makes the next one likelier. A sampler penalty cannot see this: it
// works on tokens within a window, and the habit is a phrase that recurs across
// replies, most of which have left the window by the time it comes round again.
//
// What can see it is reading the recent replies as text. Overused finds the
// phrasing a character has already leaned on, so the closing block can name it
// and ask for something else. It is also the instrument the evaluation uses to
// count repetition, so the fix and the measurement agree about what repetition
// is.

// repetitionScanReplies is how many of the character's recent replies are
// compared. Six is long enough for a habit to show twice and short enough that
// a phrase from half a scene ago, which may be a deliberate callback, is left
// alone.
const repetitionScanReplies = 6

// maxOverused bounds how many phrases the closing block names. Past about
// eight the list stops being a warning and becomes a vocabulary lesson, and a
// long list of phrases is itself text the model may copy.
const maxOverused = 8

// Repetition is what a character has been repeating.
type Repetition struct {
	// Phrases are word sequences that appeared in two or more recent replies,
	// longest and most repeated first.
	Phrases []string
	// Opening is set when most recent replies began the same way, and holds
	// that beginning.
	Opening string
	// Swears are swear words the recent replies keep coming back to. A single
	// word is invisible to the phrase finder, and it is the commonest habit of
	// all in a character written to swear: the same one, line after line.
	Swears []string
}

// Empty reports whether there is nothing worth mentioning.
func (r Repetition) Empty() bool {
	return len(r.Phrases) == 0 && r.Opening == "" && len(r.Swears) == 0
}

// Overused reads the character's recent replies and returns what they keep
// saying.
func Overused(history []ollama.Message) Repetition {
	var raw []string
	for i := len(history) - 1; i >= 0 && len(raw) < repetitionScanReplies; i-- {
		if history[i].Role != ollama.RoleAssistant {
			continue
		}
		if body := strings.TrimSpace(history[i].Content); body != "" {
			raw = append(raw, body)
		}
	}
	if len(raw) < 2 {
		return Repetition{}
	}
	return Repetition{
		Phrases: repeatedPhrases(raw),
		Opening: repeatedOpening(raw),
		Swears:  repeatedSwears(raw),
	}
}

// swearRoots maps each form of a swear word to the word it is a form of, so
// "fucking" and "fucked" count as one habit. Deliberately the common ones: the
// point is to notice a word being leaned on, not to police vocabulary, and a
// character who swears with range is doing exactly what was asked.
var swearRoots = func() map[string]string {
	m := map[string]string{}
	for root, forms := range map[string]string{
		"fuck":    "fuck fucks fucking fucked fucker fuckin",
		"shit":    "shit shits shitty shitting bullshit",
		"damn":    "damn damned dammit damnit goddamn goddamned goddammit",
		"hell":    "hell",
		"bastard": "bastard bastards",
		"bitch":   "bitch bitches bitching",
		"crap":    "crap crappy",
		"ass":     "ass asshole assholes",
		"piss":    "piss pissed pissing",
		"christ":  "christ",
		"bloody":  "bloody",
	} {
		for _, f := range strings.Fields(forms) {
			m[f] = root
		}
	}
	return m
}()

// repeatedSwears reports swear words that turn up in most of the recent
// replies. Most, not two: a character who swears casually will say the same
// word twice in a scene and that is how people talk. Three replies out of the
// last six, or four uses in the last three, is a word standing in for the
// sentence it should have been.
func repeatedSwears(replies []string) []string {
	inReplies := map[string]int{}
	recentUses := map[string]int{}
	for i, r := range replies {
		seen := map[string]bool{}
		for _, w := range wordsOf(r) {
			root, ok := swearRoots[w]
			if !ok {
				continue
			}
			if !seen[root] {
				seen[root] = true
				inReplies[root]++
			}
			if i < 3 {
				recentUses[root]++
			}
		}
	}
	var out []string
	for root, n := range inReplies {
		if n >= 3 || recentUses[root] >= 4 {
			out = append(out, root)
		}
	}
	sort.Strings(out)
	return out
}

// repeatedPhrases finds word sequences that occur in at least two different
// replies.
//
// It compares every pair of replies and keeps the maximal runs of words they
// share, rather than counting fixed-length pieces. Fixed lengths report one
// habit as a pile of shifted fragments ("the ghost of a smile", "ghost of a
// smile touched", "a smile touched her lips"), each of which is a slot in a
// short list spent on the same thing.
func repeatedPhrases(replies []string) []string {
	split := make([][][]string, len(replies))
	for i, r := range replies {
		split[i] = sentencesOf(r)
	}
	where := map[string]map[int]bool{}
	for i := 0; i < len(split); i++ {
		for j := i + 1; j < len(split); j++ {
			for _, a := range split[i] {
				for _, b := range split[j] {
					for _, run := range sharedRuns(a, b) {
						phrase := trimGrammar(run)
						if len(phrase) < minPhraseWords || !distinctive(phrase) {
							continue
						}
						key := strings.Join(phrase, " ")
						if where[key] == nil {
							where[key] = map[int]bool{}
						}
						where[key][i] = true
						where[key][j] = true
					}
				}
			}
		}
	}

	type candidate struct {
		words []string
		times int
	}
	var cands []candidate
	for key, rs := range where {
		cands = append(cands, candidate{strings.Fields(key), len(rs)})
	}
	// Longest and most repeated first, so the whole phrase is chosen before any
	// overlapping piece of it; alphabetical last only to make the order stable.
	sort.Slice(cands, func(i, j int) bool {
		a, b := len(cands[i].words)*cands[i].times, len(cands[j].words)*cands[j].times
		if a != b {
			return a > b
		}
		if len(cands[i].words) != len(cands[j].words) {
			return len(cands[i].words) > len(cands[j].words)
		}
		return strings.Join(cands[i].words, " ") < strings.Join(cands[j].words, " ")
	})

	var kept [][]string
	for _, c := range cands {
		if overlapsAny(c.words, kept) {
			continue
		}
		kept = append(kept, c.words)
		if len(kept) == maxOverused {
			break
		}
	}
	out := make([]string, len(kept))
	for i, k := range kept {
		out[i] = strings.Join(k, " ")
	}
	return out
}

// sharedRuns returns the maximal runs of consecutive words that two sentences
// have in common, at least minPhraseWords long.
func sharedRuns(a, b []string) [][]string {
	if len(a) < minPhraseWords || len(b) < minPhraseWords {
		return nil
	}
	// prev[y] is the length of the common run ending at a[x-1], b[y-1].
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	var out [][]string
	for x := 1; x <= len(a); x++ {
		for y := 1; y <= len(b); y++ {
			if a[x-1] == b[y-1] {
				cur[y] = prev[y-1] + 1
			} else {
				cur[y] = 0
			}
		}
		// A run is maximal where it cannot be extended by the next pair.
		for y := 1; y <= len(b); y++ {
			n := cur[y]
			if n < minPhraseWords {
				continue
			}
			extends := x < len(a) && y < len(b) && a[x] == b[y]
			if !extends {
				out = append(out, a[x-n:x])
			}
		}
		prev, cur = cur, prev
	}
	return out
}

// trimGrammar takes the function words off both ends of a run, keeping a
// leading article: "and the ghost of a smile touched her" is the habit "the
// ghost of a smile touched".
func trimGrammar(run []string) []string {
	for len(run) > 0 && stopword[run[len(run)-1]] {
		run = run[:len(run)-1]
	}
	for len(run) > 1 && stopword[run[0]] && !isArticle(run[0]) {
		run = run[1:]
	}
	return run
}

func isArticle(w string) bool { return w == "a" || w == "an" || w == "the" }

// overlapsAny reports whether a phrase is mostly a phrase already chosen: they
// share a run of words covering at least half of it.
func overlapsAny(words []string, kept [][]string) bool {
	for _, k := range kept {
		longest := 0
		for _, run := range sharedRunsAnyLength(words, k) {
			if len(run) > longest {
				longest = len(run)
			}
		}
		if longest*2 >= len(words) {
			return true
		}
	}
	return false
}

// sharedRunsAnyLength is sharedRuns without the minimum, for comparing two
// short phrases with each other.
func sharedRunsAnyLength(a, b []string) [][]string {
	best := 0
	end := 0
	for x := range a {
		for y := range b {
			n := 0
			for x+n < len(a) && y+n < len(b) && a[x+n] == b[y+n] {
				n++
			}
			if n > best {
				best, end = n, x+n
			}
		}
	}
	if best == 0 {
		return nil
	}
	return [][]string{a[end-best : end]}
}

// minPhraseWords is the shortest habit worth naming: "a slow breath".
const minPhraseWords = 3

// distinctive reports whether a word sequence is a choice rather than grammar.
// "and she said" repeats in every scene ever written and nobody notices it;
// "a small and deliberate violence" repeated twice is the thing a reader sees.
// So a phrase has to carry at least two words that are not function words, and
// cannot begin or end on one: "of the rain" is a fragment, "the rain" is not a
// habit.
func distinctive(words []string) bool {
	if stopword[words[len(words)-1]] || (stopword[words[0]] && !isArticle(words[0])) {
		return false
	}
	content := 0
	for _, w := range words {
		if !stopword[w] {
			content++
		}
	}
	return content >= 2
}

// repeatedOpening reports how most recent replies began, when they keep
// beginning the same way. Two words, because one is noise: a roleplay reply
// opening "She" is ordinary, opening "She tilts" four times running is a tic.
func repeatedOpening(replies []string) string {
	counts := map[string]int{}
	for _, r := range replies {
		words := wordsOf(r)
		if len(words) < 2 {
			continue
		}
		counts[words[0]+" "+words[1]]++
	}
	best, n := "", 0
	for k, v := range counts {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	if n >= 3 && n*2 > len(replies) {
		return best
	}
	return ""
}

// sentencesOf splits a reply into sentences of lowercased words, with the
// markup gone. Sentences, so a phrase never runs across a full stop and comes
// out as the end of one thought joined to the start of another.
func sentencesOf(s string) [][]string {
	var out [][]string
	var cur []string
	var word strings.Builder
	flushWord := func() {
		if word.Len() > 0 {
			cur = append(cur, word.String())
			word.Reset()
		}
	}
	flushSentence := func() {
		flushWord()
		if len(cur) > 0 {
			out = append(out, cur)
			cur = nil
		}
	}
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(unicode.ToLower(r))
		case r == '\'' || r == '’':
			// Inside a word it is a contraction and belongs to it; "don't"
			// is one word, not two.
			if word.Len() > 0 {
				word.WriteRune('\'')
			}
		case r == '.' || r == '!' || r == '?' || r == '\n' || r == ';' || r == ':' || r == '…':
			flushSentence()
		default:
			flushWord()
		}
	}
	flushSentence()
	return out
}

// wordsOf is a reply's words in order, across sentences.
func wordsOf(s string) []string {
	var out []string
	for _, sentence := range sentencesOf(s) {
		out = append(out, sentence...)
	}
	return out
}

// stopword is the grammar of English: words whose repetition is not a choice.
var stopword = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an the and or but if then so as at by for from in into of off on onto
		out over to up with without about above after again against all am any are aren't be because been
		before being below between both can can't could couldn't did didn't do does doesn't doing don't down
		during each few further had hadn't has hasn't have haven't having he he'd he'll he's her here here's hers
		herself him himself his how how's i i'd i'll i'm i've is isn't it it's its itself just let's me more most
		my myself no nor not now once only other ought our ours ourselves own same she she'd she'll she's should
		shouldn't some such than that that's their theirs them themselves there there's these they they'd they'll
		they're they've this those through too under until very was wasn't we we'd we'll we're we've were weren't
		what what's when when's where where's which while who who's whom why why's will won't would wouldn't you
		you'd you'll you're you've your yours yourself yourselves still even yet also back like one`) {
		m[w] = true
	}
	return m
}()

// RepetitionScore is the share of a reply's distinctive phrases that already
// appeared in the replies before it: zero is fresh, one is a rerun.
//
// The measurement, not the fix. It exists so the evaluation and the closing
// block count the same thing, and it lives next to Overused so the definition
// of a phrase cannot drift between them.
func RepetitionScore(reply string, earlier []string) float64 {
	before := map[string]bool{}
	for _, e := range earlier {
		for _, sentence := range sentencesOf(e) {
			for i := 0; i+minPhraseWords <= len(sentence); i++ {
				gram := sentence[i : i+minPhraseWords]
				if distinctive(gram) {
					before[strings.Join(gram, " ")] = true
				}
			}
		}
	}
	total, repeated := 0, 0
	for _, sentence := range sentencesOf(reply) {
		for i := 0; i+minPhraseWords <= len(sentence); i++ {
			gram := sentence[i : i+minPhraseWords]
			if !distinctive(gram) {
				continue
			}
			total++
			if before[strings.Join(gram, " ")] {
				repeated++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(repeated) / float64(total)
}
