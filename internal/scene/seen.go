package scene

import (
	"fmt"
	"strings"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/world"
)

// What the Model Sees: which lorebook entries go with the next turn and what
// brought each in, and which earlier moments are pinned and recalled. The
// memory meter says how much room each part takes; this says what is in it,
// so when a character forgets something, or talks about a part of the world
// that has nothing to do with the scene, you can see why. Character.AI,
// AI Dungeon and Msty show the same, and SillyTavern has an extension for it.
//
// Built from the same choices the next turn makes, not an estimate of them.

// Seen is what the next turn of a scene sends, besides the transcript.
type Seen struct {
	// World is the name of the world whose lorebook is read. Empty when the
	// scene has none.
	World    string       `json:"world,omitempty"`
	Lore     []SeenLore   `json:"lore"`
	Pinned   []SeenMoment `json:"pinned"`
	Recalled []SeenMoment `json:"recalled"`
	// Record is how long the scene's record is, in characters, 0 when the
	// scene has not needed one.
	Record int `json:"record"`
}

// SeenLore is one lorebook entry the scene triggered.
type SeenLore struct {
	Name string `json:"name"`
	// Why says what brought it in, as a sentence.
	Why string `json:"why"`
	// Left is an entry triggered but left out, for lack of room.
	Left bool `json:"left,omitempty"`
}

// SeenMoment is an earlier moment the next turn is reminded of.
type SeenMoment struct {
	ID   int64  `json:"id"`
	Who  string `json:"who"`
	Text string `json:"text"`
}

// WhatItSees says what the next turn of a scene sends besides the
// transcript, as BuildFor would make it.
func WhatItSees(st *store.Store, cfg store.Config, ch store.Chat, cast []chars.Character, hist []ollama.Message) Seen {
	seen := Seen{Record: len(strings.TrimSpace(ch.Summary)), Lore: []SeenLore{}, Pinned: []SeenMoment{}, Recalled: []SeenMoment{}}
	if len(cast) == 0 {
		return seen
	}
	p := Persona(cfg)
	host, budget := cast[0], Budget(cfg, cast[0], p)
	if len(cast) > 1 {
		host, budget = groupHost(cast), GroupBudget(cfg, cast, p, Relations(st, cast))
	}
	if w, hits, ok := loreHits(st, host, hist, budget.Lore); ok {
		seen.World = w.Name
		for _, h := range hits {
			seen.Lore = append(seen.Lore, SeenLore{Name: h.Entry.Name, Why: whyLore(h), Left: h.Dropped})
		}
	}
	byID := make(map[int64]string, len(cast))
	for _, c := range cast {
		byID[c.ID] = c.Name
	}
	nameOf := func(id int64) string { return byID[id] }
	pins, kept := recall(st, ch, hist, budget.Memory)
	userName := userNameOf(cfg)
	for _, m := range pins {
		seen.Pinned = append(seen.Pinned, SeenMoment{ID: m.ID, Who: speakerOf(m, nameOf, cast[0].Name, userName),
			Text: chars.Substitute(m.Content, cast[0].Name, userName)})
	}
	for _, m := range kept {
		seen.Recalled = append(seen.Recalled, SeenMoment{ID: m.ID, Who: speakerOf(m, nameOf, cast[0].Name, userName),
			Text: chars.Substitute(m.Content, cast[0].Name, userName)})
	}
	return seen
}

// whyLore says what brought an entry in.
func whyLore(h world.Hit) string {
	switch {
	case h.Via != "":
		return fmt.Sprintf("Named in %s.", h.Via)
	case h.Key != "":
		return fmt.Sprintf("“%s” was mentioned.", h.Key)
	default:
		return "Always sent."
	}
}
