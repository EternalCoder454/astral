// Package world holds the setting a scene takes place in: the world itself,
// and the lorebook of things that are true within it.
//
// A lorebook is not simply extra prompt text. Everything it knows cannot fit
// in a context window, and most of it is irrelevant to any given moment, so
// entries carry keys and only the ones the conversation is currently touching
// are sent. A world can therefore be far larger than the model can hold, and
// still be consistent whenever a part of it comes up.
package world

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
	"unicode"
)

// World is a setting. Characters belong to one, and a scene inherits it from
// the character being played.
type World struct {
	ID          int64
	Name        string
	Description string
	// Rules are what is always true here: what can and cannot happen, who
	// holds power, what a person in this world takes for granted. Unlike a
	// lore entry it is not waiting for a keyword, because it is not about a
	// subject that might come up. It is the ground everything else stands on,
	// so it is sent with the setting on every turn.
	//
	// That is also why it is bounded. See rulesChars.
	Rules     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Entry is one thing that is true in a world: a person, a place, an object, a
// piece of history.
type Entry struct {
	ID      int64
	WorldID int64
	// Name is the label shown in lists. It is also the identity used when the
	// model updates an entry, so renaming one and re-learning it produces two
	// entries rather than a merge.
	Name string
	// Keys are what a conversation has to mention for this entry to be sent.
	Keys []string
	// Content is the lore itself, written as fact.
	Content string
	Enabled bool
	// Constant entries are sent every turn regardless of what was said. Use
	// sparingly: they cost their budget on every single message.
	Constant bool
	// Auto marks an entry the model wrote by itself, so it can be reviewed,
	// and so a hand-written entry is never silently overwritten by one.
	Auto bool
	// Priority breaks ties when the budget runs out. Higher survives.
	Priority int
	// Chance is the share of turns, in percent, on which a triggered entry is
	// sent. 1 to 100, and 0 reads as 100, so an entry written without ever
	// hearing of this field is still sent whenever it is triggered. A scene
	// where every rumour turns up every time is a scene where none is a rumour.
	// Constant entries ignore it.
	Chance int
	// Wait is how many messages the scene must have before this entry is sent.
	// It keeps a secret out of the first scene and a late reveal out of the
	// early ones. Constant entries ignore it.
	Wait int
	// Group names a set of entries of which at most one is sent on a turn: the
	// first of the triggered ones in priority order. Empty for an entry that
	// stands alone. Unlike the other two it binds constant entries as well.
	Group string
	// Confidence is how sure the model was when it wrote this, 0 to 1, and is
	// 0 for anything written by hand. Below AutoApplyConfidence an entry is
	// stored disabled and waits for review.
	Confidence float64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Defaults for how much of a conversation is scanned and how much lore may be
// injected. Both are in characters rather than tokens, for the same reason the
// rest of Astral measures in characters: counting real tokens would mean
// shipping a tokenizer per model.
const (
	// ScanDepthChars is how far back a conversation is searched for keys.
	// Deep enough to catch a subject still being discussed, shallow enough
	// that something mentioned once an hour ago stops being injected.
	ScanDepthChars = 4000

	// BudgetChars caps how much lore goes out on a turn. Lore competes with
	// the transcript for the same context, and a world that fills the window
	// with encyclopedia entries leaves no room for the scene itself.
	BudgetChars = 3000
)

// UnknownLength is a scene length for a caller with no scene behind it, such
// as a test or an evaluation that scores a lorebook against a piece of text.
// Nothing then waits and nothing is left to chance, because both need to know
// which turn this is.
const UnknownLength = -1

// Match selects the entries a conversation has triggered, in the order they
// should be injected.
//
// recent is the tail of the transcript. Constant entries are always included;
// the rest need one of their keys to appear. Everything is then trimmed to
// budget, highest priority first, so a world larger than the window degrades
// by dropping its least important lore rather than by failing.
//
// It has no scene to count, so it is Explain for an UnknownLength: every entry
// is past its Wait and none is held back by its Chance.
func Match(entries []Entry, recent string, budget int) []Entry {
	var out []Entry
	for _, h := range Explain(entries, recent, budget, UnknownLength) {
		if h.Sent() {
			out = append(out, h.Entry)
		}
	}
	return out
}

// Hit is an entry a conversation triggered, and why, for showing what the
// model was sent and what brought each entry in.
type Hit struct {
	Entry Entry
	// Key is the word in the scene that brought the entry in. Empty for an
	// entry sent always, and for one brought in by another.
	Key string
	// Via is the entry whose text named this one, when that is what brought
	// it in, and Key is then the word in that entry.
	Via string
	// Dropped is an entry that was triggered but did not fit the budget.
	Dropped bool
	// Skipped is why a triggered entry was left out although it would have
	// fitted, as a sentence: it is still waiting for the scene to grow, it lost
	// its roll this turn, or another entry in its group went instead. Empty for
	// an entry that was not.
	Skipped string
}

// Sent reports whether the entry goes out with the turn.
func (h Hit) Sent() bool { return !h.Dropped && h.Skipped == "" }

// Explain is Match, saying why: every entry the conversation triggered, in
// the order they would go, with what brought each in and whether it was sent.
//
// messages is how many messages the scene has, for Wait and for the roll of
// Chance, or UnknownLength. The roll is a function of the entry and of that
// number, never of chance, so the same turn decides the same way every time it
// is worked out: what the scene is shown as sent is what is sent.
func Explain(entries []Entry, recent string, budget int, messages int) []Hit {
	if budget <= 0 {
		budget = BudgetChars
	}
	if len(recent) > ScanDepthChars {
		recent = recent[len(recent)-ScanDepthChars:]
	}
	haystack := strings.ToLower(recent)

	var hits []Hit
	in := make(map[int]bool, len(entries))
	for i, e := range entries {
		if !e.Enabled || strings.TrimSpace(e.Content) == "" {
			continue
		}
		if e.Constant {
			hits = append(hits, Hit{Entry: e})
			in[i] = true
		} else if k := matchKey(haystack, e.Keys); k != "" {
			hits = append(hits, Hit{Entry: e, Key: k, Skipped: heldBack(e, messages)})
			in[i] = true
		}
	}
	byPriority(hits)
	oneOfEachGroup(hits)

	// Entries the matched ones mention, and the ones those mention: a
	// harbourmaster's entry that names the guild she answers to brings the
	// guild's entry with it, so the model is not told about somebody's
	// allegiance and left to invent what it is allegiance to. Two levels, and
	// after every direct match, so when the budget runs out it is these that
	// go first.
	//
	// Only what is sent names anything. An entry left out for its Wait, its
	// Chance or its group is not in the prompt, so what it says brings nothing
	// with it, and it is not brought back by another's mention either, or the
	// roll would be a way round itself.
	from := sending(hits)
	for level := 0; level < cascadeLevels && len(from) > 0; level++ {
		mentions := make([]string, len(from))
		for j, h := range from {
			mentions[j] = strings.ToLower(h.Entry.Content)
		}
		var next []Hit
		for i, e := range entries {
			if in[i] || !e.Enabled || e.Constant || strings.TrimSpace(e.Content) == "" {
				continue
			}
			for j, text := range mentions {
				if k := matchKey(text, e.Keys); k != "" {
					next = append(next, Hit{Entry: e, Key: k, Via: from[j].Entry.Name, Skipped: heldBack(e, messages)})
					in[i] = true
					break
				}
			}
		}
		byPriority(next)
		at := len(hits)
		hits = append(hits, next...)
		// Settled again with these in, because a mentioned entry can outrank
		// one that came in before it. What that one names has been looked for
		// already, which is the rarer case and is left as it is.
		oneOfEachGroup(hits)
		from = sending(hits[at:])
	}

	used := 0
	for i := range hits {
		if hits[i].Skipped != "" {
			continue // not being sent, so it takes no room
		}
		cost := len(hits[i].Entry.Name) + len(hits[i].Entry.Content) + 4
		if used+cost > budget {
			hits[i].Dropped = true // skip this one, but a later cheaper entry may still fit
			continue
		}
		used += cost
	}
	return hits
}

// sending is the hits that are not left out for a reason of their own.
func sending(hits []Hit) []Hit {
	var out []Hit
	for _, h := range hits {
		if h.Skipped == "" {
			out = append(out, h)
		}
	}
	return out
}

// heldBack says why a triggered entry is not sent this turn, or "" when
// nothing holds it back. A constant entry is never held back here: it is sent
// whatever else is true, which is what it is for.
func heldBack(e Entry, messages int) string {
	if e.Constant || messages < 0 {
		return ""
	}
	if e.Wait > messages {
		if e.Wait == 1 {
			return "Waits for 1 message."
		}
		return fmt.Sprintf("Waits for %d messages.", e.Wait)
	}
	if c := e.Odds(); c < 100 && roll(e.ID, messages) >= c {
		return fmt.Sprintf("Chance of %d percent, not this turn.", c)
	}
	return ""
}

// Odds is the entry's Chance as a percentage from 1 to 100, reading the zero
// an entry has when nobody has set it as always.
func (e Entry) Odds() int {
	if e.Chance < 1 || e.Chance > 100 {
		return 100
	}
	return e.Chance
}

// MaxGroupChars is how long a group's name may be. It is shown beside the
// entry, in a list, and a name longer than that is a sentence.
const MaxGroupChars = 40

// CleanGroup is a group's name as it is kept: on one line, without stray
// spaces, and not longer than MaxGroupChars.
func CleanGroup(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > MaxGroupChars {
		s = strings.TrimSpace(string(r[:MaxGroupChars]))
	}
	return s
}

// UsesCount reports whether any entry looks at the length of the scene, so a
// caller that has to go to the database for it can skip that when none does.
func UsesCount(entries []Entry) bool {
	for _, e := range entries {
		if !e.Constant && (e.Wait > 0 || e.Odds() < 100) {
			return true
		}
	}
	return false
}

// roll is a number from 0 to 99 for one entry on one turn, and the entry is
// sent when it is under its Chance.
//
// It is a hash rather than a random number so that the scene decides the turn
// and not the moment somebody asked. What the Model Sees, the memory meter and
// the reply itself each work out what a turn sends, and an entry that showed
// in one and was missing from the next would make the first a lie.
func roll(id int64, messages int) int {
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:8], uint64(id))
	binary.LittleEndian.PutUint64(b[8:], uint64(messages))
	h := fnv.New64a()
	h.Write(b[:])
	return int(h.Sum64() % 100)
}

// oneOfEachGroup leaves out all but the first entry of each group, in priority
// order and then by id, among those still being sent. Groups are compared
// without regard to case, so "Rumours" and "rumours" are one.
//
// Wait and Chance have been settled by now, so an entry that did not roll in
// cannot take the place of one that did.
func oneOfEachGroup(hits []Hit) {
	first := map[string]int{}
	for i, h := range hits {
		g := strings.ToLower(strings.TrimSpace(h.Entry.Group))
		if g == "" || h.Skipped != "" {
			continue
		}
		j, taken := first[g]
		if !taken {
			first[g] = i
			continue
		}
		loser := i
		if ranksAbove(h.Entry, hits[j].Entry) {
			first[g] = i
			loser = j
		}
		hits[loser].Skipped = "Another entry in the group " + strings.TrimSpace(hits[loser].Entry.Group) + " went instead."
	}
}

// ranksAbove is byPriority's order for two entries: a goes first.
func ranksAbove(a, b Entry) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	return a.ID < b.ID
}

// cascadeLevels is how far a mention is followed: an entry matched by the
// scene can bring in the entries it names, and they the entries they name,
// and no further. Past two the chain is rarely about the scene any more.
const cascadeLevels = 2

// byPriority orders entries highest priority first, then oldest, so the order
// a turn sees is stable between messages rather than shuffling.
func byPriority(hs []Hit) {
	sort.SliceStable(hs, func(i, j int) bool {
		return ranksAbove(hs[i].Entry, hs[j].Entry)
	})
}

// matchKey is the first key that appears in the already-lowercased haystack,
// as it was written, or "" when none does.
func matchKey(haystack string, keys []string) string {
	for _, k := range keys {
		low := strings.ToLower(strings.TrimSpace(k))
		if low == "" {
			continue
		}
		if containsWord(haystack, low) {
			return strings.TrimSpace(k)
		}
	}
	return ""
}

// containsWord looks for key at a word boundary.
//
// A plain substring search is not good enough: a key of "Ash" would fire on
// "cash", "ashamed" and "flash", and an entry that injects itself constantly
// for no reason is worse than one that never fires, because it costs budget
// that real matches needed.
func containsWord(haystack, key string) bool {
	from := 0
	for {
		i := strings.Index(haystack[from:], key)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(key)
		if isBoundary(haystack, start-1) && isBoundary(haystack, end) {
			return true
		}
		from = start + 1
	}
}

// isBoundary reports whether the byte at i is absent or not part of a word.
// Multi-word keys are handled by this too: only the outer edges are checked,
// so "Kestrel Bay" matches inside a sentence without its own space tripping it.
func isBoundary(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	r := rune(s[i])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// Render turns matched entries into the block sent to the model.
// rulesChars bounds the rules, which are sent on every turn.
//
// The lore block has a budget and the entries share it. Something unbounded
// and always present would take that budget from the entries, which are the
// part that changes with the scene, so the floor a world stands on is not
// allowed to become the whole of it.
const rulesChars = 700

func Render(w World, entries []Entry) string {
	name := strings.TrimSpace(w.Name)
	desc := strings.TrimSpace(w.Description)
	rules := truncateRules(strings.TrimSpace(w.Rules))
	// A world with nothing written in it and nothing matched is nothing to
	// say. Anything else is worth sending.
	if len(entries) == 0 && name == "" && desc == "" && rules == "" {
		return ""
	}

	var b strings.Builder
	if name != "" {
		b.WriteString("The scene takes place in ")
		b.WriteString(name)
		if desc != "" {
			b.WriteString(". ")
			b.WriteString(desc)
		} else {
			b.WriteString(".")
		}
		b.WriteString("\n\n")
	} else if desc != "" {
		b.WriteString(desc)
		b.WriteString("\n\n")
	}
	if rules != "" {
		b.WriteString("How this world works (true everywhere in it, and always):\n")
		b.WriteString(rules)
		b.WriteString("\n\n")
	}
	if len(entries) > 0 {
		b.WriteString("What is true here (established fact, treat it as already known):\n")
		for _, e := range entries {
			b.WriteString("- ")
			if n := strings.TrimSpace(e.Name); n != "" {
				b.WriteString(n)
				b.WriteString(": ")
			}
			b.WriteString(strings.Join(strings.Fields(e.Content), " "))
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// truncateRules bounds the rules, cutting at a line end so a rule is not left
// half-stated.
func truncateRules(s string) string {
	if len(s) <= rulesChars {
		return s
	}
	cut := s[:rulesChars]
	if i := strings.LastIndexAny(cut, ".\n"); i > rulesChars/2 {
		return strings.TrimSpace(cut[:i+1])
	}
	return strings.TrimSpace(cut)
}

// RecentText joins the tail of a transcript into the text Match scans.
func RecentText(turns []string) string {
	var b strings.Builder
	for i := len(turns) - 1; i >= 0; i-- {
		if b.Len()+len(turns[i]) > ScanDepthChars {
			break
		}
		b.WriteString(turns[i])
		b.WriteByte('\n')
	}
	return b.String()
}
