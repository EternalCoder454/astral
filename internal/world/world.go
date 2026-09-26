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

// Match selects the entries a conversation has triggered, in the order they
// should be injected.
//
// recent is the tail of the transcript. Constant entries are always included;
// the rest need one of their keys to appear. Everything is then trimmed to
// budget, highest priority first, so a world larger than the window degrades
// by dropping its least important lore rather than by failing.
func Match(entries []Entry, recent string, budget int) []Entry {
	if budget <= 0 {
		budget = BudgetChars
	}
	if len(recent) > ScanDepthChars {
		recent = recent[len(recent)-ScanDepthChars:]
	}
	haystack := strings.ToLower(recent)

	var hits []Entry
	for _, e := range entries {
		if !e.Enabled || strings.TrimSpace(e.Content) == "" {
			continue
		}
		if e.Constant || matches(haystack, e.Keys) {
			hits = append(hits, e)
		}
	}

	// Highest priority first, then oldest, so the order a turn sees is stable
	// between messages rather than shuffling with map iteration.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Priority != hits[j].Priority {
			return hits[i].Priority > hits[j].Priority
		}
		return hits[i].ID < hits[j].ID
	})

	out := make([]Entry, 0, len(hits))
	used := 0
	for _, e := range hits {
		cost := len(e.Name) + len(e.Content) + 4
		if used+cost > budget {
			continue // skip this one, but a later cheaper entry may still fit
		}
		used += cost
		out = append(out, e)
	}
	return out
}

// matches reports whether any key appears in the already-lowercased haystack.
func matches(haystack string, keys []string) bool {
	for _, k := range keys {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		if containsWord(haystack, k) {
			return true
		}
	}
	return false
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
