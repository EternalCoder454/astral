// Package prompts is every prompt Astral sends a model, in one place, and the
// versions of them you have rewritten.
//
// The prompts themselves stay beside the code that uses them, where the
// reasons for each line are written down. What lives here is the list: each
// package registers its prompts by name, and asks for them back through Text,
// which answers with your version when there is one and Astral's when there is
// not. That is what lets the Prompt Optimizer read any of them, and lets a
// rewrite take effect everywhere the prompt is used without the code that
// sends it knowing that anything changed.
//
// A leaf package on purpose: everything that builds a prompt imports it, so it
// imports nothing of Astral's. Your versions are stored in the database and
// handed in with SetOverrides.
package prompts

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Prompt is one of the prompts Astral sends.
type Prompt struct {
	// ID is the stable name a rewrite is stored under. Never change one: a
	// saved rewrite would stop applying.
	ID string
	// Name is what it is called in the interface, in Title Case.
	Name string
	// Group is which part of Astral uses it.
	Group string
	// About says what it is for, when it is sent and with what around it, for
	// the person reading the list and for the optimizer rewriting it.
	About string
	// Keep is what any rewrite has to keep for the code around the prompt to
	// go on working: placeholders it fills in, words it looks for in the reply.
	Keep string
	// Default is Astral's own text.
	Default string
	// Slots are what Astral fills in for this prompt beyond the ones its own
	// text uses, so a rewrite may use them.
	Slots []string
	// Anchors are phrases a rewrite has to keep word for word: the ones that
	// were measured, and the ones a rewrite that tidies up is likely to lose.
	// The optimizer is told them, and a rewrite without one is flagged.
	Anchors []string

	// seq is the order it was registered in, which within a package is the
	// order it is declared in, most important first.
	seq int
}

var (
	mu        sync.RWMutex
	registry  = map[string]Prompt{}
	overrides = map[string]string{}
)

// Register adds a prompt to the list and returns its ID, so a package can
// register and name a prompt in one declaration:
//
//	var promptFraming = prompts.Register(prompts.Prompt{ID: "scene.framing", ...})
func Register(p Prompt) string {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[p.ID]; dup {
		panic("prompts: " + p.ID + " registered twice")
	}
	p.seq = len(registry)
	registry[p.ID] = p
	return p.ID
}

// Text is the prompt to send: your version when there is one, Astral's when
// there is not. An unknown ID is a programming error and answers empty.
func Text(id string) string {
	mu.RLock()
	defer mu.RUnlock()
	if t, ok := overrides[id]; ok {
		return t
	}
	return registry[id].Default
}

// Overridden reports whether a prompt has been rewritten.
func Overridden(id string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := overrides[id]
	return ok
}

// SetOverrides replaces every rewrite at once, with the set the database holds.
// Rewrites of prompts that no longer exist are kept, and ignored, so a
// downgrade and upgrade does not lose them.
func SetOverrides(m map[string]string) {
	next := make(map[string]string, len(m))
	for id, t := range m {
		if strings.TrimSpace(t) != "" {
			next[id] = t
		}
	}
	mu.Lock()
	overrides = next
	mu.Unlock()
}

// Get returns one prompt, with Default holding Astral's text.
func Get(id string) (Prompt, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[id]
	return p, ok
}

// groupOrder is the order the groups are listed in: roughly how often each is
// sent, so the prompts that shape every reply come first.
var groupOrder = []string{"Scenes", "Conversation", "Designers", "Pictures", "Memory", "Worlds", "Knowledge", "Optimizer"}

// All returns every prompt, grouped and in a stable order.
func All() []Prompt {
	mu.RLock()
	out := make([]Prompt, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	mu.RUnlock()
	rank := func(g string) int {
		for i, x := range groupOrder {
			if x == g {
				return i
			}
		}
		return len(groupOrder)
	}
	sort.Slice(out, func(i, j int) bool {
		gi, gj := rank(out[i].Group), rank(out[j].Group)
		if gi != gj {
			return gi < gj
		}
		return out[i].seq < out[j].seq
	})
	return out
}

// Sent is the most recent request of one kind, as it actually went to a model:
// the registered prompts with everything the code put around them, a
// character's card, the recap, the lore and the reminder at the end. It is what
// the optimizer reads to see a prompt in the company it is sent in. Kept in
// memory only, and only the latest of each kind.
type Sent struct {
	Kind string // stable, for the optimizer to ask by: "scene", "group", ...
	Name string // what it is called, in Title Case
	Text string
	At   time.Time
}

var (
	sentMu sync.Mutex
	sent   = map[string]Sent{}
)

// RecordSent keeps the latest request of a kind.
func RecordSent(kind, name, text string) {
	sentMu.Lock()
	sent[kind] = Sent{Kind: kind, Name: name, Text: text, At: time.Now()}
	sentMu.Unlock()
}

// LastSent returns the latest request of every kind recorded, in kind order.
func LastSent() []Sent {
	sentMu.Lock()
	out := make([]Sent, 0, len(sent))
	for _, s := range sent {
		out = append(out, s)
	}
	sentMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// SentOf returns the latest request of one kind.
func SentOf(kind string) (Sent, bool) {
	sentMu.Lock()
	defer sentMu.Unlock()
	s, ok := sent[kind]
	return s, ok
}
