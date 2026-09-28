package chars

import (
	"strings"
)

// Profile is one of the people you play as.
//
// There used to be one of you: a name and a paragraph in Settings. People play
// more than one person, a smuggler in one scene and a knight in the next, and a
// paragraph is where an age, a height and a species go to be forgotten. So you
// have as many as you like, each with the parts a scene actually uses, and a
// chat remembers which of them it was started with.
type Profile struct {
	ID          int64
	Name        string
	Age         string
	Gender      string
	Race        string
	Appearance  string
	Personality string
	Background  string
	// Details is anything else: a free field for what fits none of the others.
	Details string
	Accent  int
}

// Description is the profile written out for the model, one labelled part per
// line, which is how the character's own card reaches it too. Empty parts are
// left out rather than sent as blanks.
func (p Profile) Description() string {
	var b strings.Builder
	line := func(label, value string) {
		if v := strings.TrimSpace(value); v != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(label + ": " + v)
		}
	}
	line("Age", p.Age)
	line("Gender", p.Gender)
	line("Race", p.Race)
	line("Appearance", p.Appearance)
	line("Personality", p.Personality)
	line("Background", p.Background)
	if d := strings.TrimSpace(p.Details); d != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(d)
	}
	return b.String()
}

// Facts is the one line a list shows under the name: age, gender and race,
// the three a glance down the list is looking for.
func (p Profile) Facts() string {
	var parts []string
	for _, s := range []string{p.Age, p.Gender, p.Race} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return strings.Join(strings.Fields(firstNonEmpty(p.Appearance, p.Personality, p.Details)), " ")
	}
	return strings.Join(parts, ", ")
}

// DisplayName is the name, or the one the model would use for someone unnamed.
func (p Profile) DisplayName() string {
	if n := strings.TrimSpace(p.Name); n != "" {
		return n
	}
	return DefaultPersonaName
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
