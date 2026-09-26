package store

import "strings"

// A rulebook is the standing instructions the model is under: one rule per line,
// each one switchable on its own.
//
// It replaces a single freeform box, and the reason is what people actually did
// with that box. Instructions accumulate — "keep replies short", "never skip
// ahead in time", "she always lies about her past" — and once there are six of
// them in one paragraph, trying one scene without the third means deleting it
// and retyping it afterwards. So nobody tries. A rule you can switch off is a
// rule you will experiment with, and a rule you can switch off is a rule you
// keep rather than lose.
//
// Where they go in the prompt is not decided here. Rendered, they occupy exactly
// the position the freeform instructions did: stated once in the system message
// and restated in the closing block, which is the position a model obeys. See
// chars.BuildSystem and chars.Anchor.

// MaxRules is how many rules a rulebook holds.
//
// Twenty, and the limit is the model's attention rather than anything here. Every
// enabled rule is sent twice per turn, and a list long enough to contradict
// itself is worse than no list: the model picks whichever half it read last.
const MaxRules = 20

// Rule is one standing instruction.
type Rule struct {
	// Text is the instruction itself, in the words the model will read. There is
	// no separate name: a rule is a sentence, and asking for a label as well
	// produces "Length" above "keep replies to two paragraphs", which is one
	// more thing to keep in step for no gain.
	Text string `json:"text"`
	// Enabled is whether it is in force. A disabled rule is kept, not deleted:
	// the whole point of the list is being able to try a scene without one.
	Enabled bool `json:"enabled"`
}

// Rules returns the rulebook in order.
func (c Config) Rules() []Rule {
	out := make([]Rule, 0, len(c.Rulebook))
	for _, r := range c.Rulebook {
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}

// RulesText is the enabled rules as the model sees them, one per line.
//
// Numbered, because a model handed a bare list of sentences treats it as prose
// to absorb and a numbered list as instructions to follow. Empty when nothing is
// enabled, so a rulebook that is entirely switched off costs no tokens and no
// heading rather than sending an empty section.
func (c Config) RulesText() string {
	var b strings.Builder
	n := 0
	for _, r := range c.Rules() {
		if !r.Enabled {
			continue
		}
		n++
		if n > 1 {
			b.WriteByte('\n')
		}
		b.WriteString(itoa(n))
		b.WriteString(". ")
		b.WriteString(strings.TrimSpace(r.Text))
	}
	return b.String()
}

// itoa avoids pulling strconv in for two digits.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// SetRules replaces the rulebook, dropping blanks and anything past the limit.
func (c *Config) SetRules(rules []Rule) {
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		r.Text = strings.TrimSpace(r.Text)
		if r.Text == "" {
			continue
		}
		if len(out) >= MaxRules {
			break
		}
		out = append(out, r)
	}
	c.Rulebook = out
}

// AddRule appends a rule, switched on, and reports whether there was room.
func (c *Config) AddRule(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if len(c.Rulebook) >= MaxRules {
		return false
	}
	c.Rulebook = append(c.Rulebook, Rule{Text: text, Enabled: true})
	return true
}

// MoveRule shifts the rule at i by delta places, and reports whether it moved.
//
// Order is worth having even though all the rules go into one block. A model
// reading a list weights the end of it, so the rule you most want obeyed belongs
// last, and finding that out means being able to try it.
func (c *Config) MoveRule(i, delta int) bool {
	j := i + delta
	if i < 0 || i >= len(c.Rulebook) || j < 0 || j >= len(c.Rulebook) {
		return false
	}
	c.Rulebook[i], c.Rulebook[j] = c.Rulebook[j], c.Rulebook[i]
	return true
}

// RemoveRule drops the rule at i.
func (c *Config) RemoveRule(i int) bool {
	if i < 0 || i >= len(c.Rulebook) {
		return false
	}
	c.Rulebook = append(c.Rulebook[:i], c.Rulebook[i+1:]...)
	return true
}

// adoptGlobalInstructions turns a freeform instruction block into rules.
//
// Run once, when a config written before the rulebook existed is loaded. The old
// box was filled a line at a time by people keeping a list in it, so a line is
// the right unit to split on, and what comes out is the list they were already
// keeping — switched on, because it was in force a moment ago.
func (c *Config) adoptGlobalInstructions() {
	if len(c.Rulebook) > 0 || strings.TrimSpace(c.GlobalInstructions) == "" {
		return
	}
	for _, line := range strings.Split(c.GlobalInstructions, "\n") {
		line = strings.TrimSpace(line)
		// Leading bullets and numbers, since the box was a list in all but name.
		line = strings.TrimLeft(line, "-*•0123456789.) \t")
		if line == "" {
			continue
		}
		if !c.AddRule(line) {
			break
		}
	}
	// Cleared, so there is one place a standing instruction lives. Two would
	// mean editing a rule in the list and finding the old copy still in force.
	c.GlobalInstructions = ""
}
