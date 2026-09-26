package store

import (
	"sort"
	"strings"

	"astral/internal/chars"
)

// How two characters know each other.
//
// A group scene put five people in a room who had never met. Each card described
// one person in isolation, because that is what a card is, so the model invented
// whatever history it needed on the spot and then forgot it. Two characters who
// have been lovers for ten years introduced themselves.
//
// A relation is one line about a pair, and it reaches the prompt only when both
// of them are in the scene. That is the whole design: it costs nothing in a
// two-hander, and in a group it is the difference between people who know each
// other and strangers being polite.

// MaxRelationChars bounds one line. A relation is "she owes him money and he has
// stopped asking", not a shared backstory; anything longer belongs in a lorebook
// entry, which is the thing built for it.
const MaxRelationChars = 400

// Relation is what one character is to another.
type Relation struct {
	// Other is the character at the far end of it, filled in by the reader.
	Other chars.Character
	// Note is the line itself, written about the pair rather than from one side:
	// "Vesper trained her, and neither of them mentions it" reads correctly in
	// both directions, where "my mentor" only reads in one.
	Note string
}

// pairOrder puts a pair in a fixed order, so one relation cannot be stored twice
// under two spellings of the same two people.
func pairOrder(a, b int64) (int64, int64) {
	if a > b {
		return b, a
	}
	return a, b
}

// SetRelation records how two characters know each other. An empty note removes
// it, because a relation with nothing to say is not one.
func (s *Store) SetRelation(a, b int64, note string) error {
	if a == b || a == 0 || b == 0 {
		return nil
	}
	lo, hi := pairOrder(a, b)
	note = strings.TrimSpace(note)
	if len(note) > MaxRelationChars {
		note = note[:MaxRelationChars]
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if note == "" {
		_, err := s.db.Exec(`DELETE FROM relations WHERE a_id = ? AND b_id = ?`, lo, hi)
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO relations (a_id, b_id, note) VALUES (?,?,?)
		ON CONFLICT(a_id, b_id) DO UPDATE SET note = excluded.note`, lo, hi, note)
	return err
}

// Relations is everyone a character has a recorded relation with.
//
// The far character is read row by row rather than joined in. A character has a
// handful of relations, the rows are small, and one join that has to interleave
// two column lists is the kind of query that breaks silently the next time a
// column is added.
func (s *Store) Relations(id int64) ([]Relation, error) {
	if id == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT CASE WHEN a_id = ? THEN b_id ELSE a_id END AS other, note
		FROM relations WHERE a_id = ? OR b_id = ?`, id, id, id)
	if err != nil {
		return nil, err
	}
	type pair struct {
		other int64
		note  string
	}
	var found []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.other, &p.note); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Relation, 0, len(found))
	for _, p := range found {
		c, err := s.Character(p.other)
		if err != nil {
			// Deleted out from under the relation. The row is tidied rather than
			// reported: a relation with nobody at one end is not a thing the
			// person can act on.
			continue
		}
		out = append(out, Relation{Other: c, Note: p.note})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Other.Name < out[j].Other.Name })
	return out, nil
}

// RelationsAmong is the notes for every pair within one set of characters.
//
// Only pairs where both are present, which is what makes this cheap enough to
// send every turn: a cast of five has ten possible pairs and usually two or three
// recorded ones, and a relation with somebody who is not in the room is noise.
func (s *Store) RelationsAmong(ids []int64) ([]chars.Relation, error) {
	if len(ids) < 2 {
		return nil, nil
	}
	// Both ends have to be in the set, which one IN clause on each side says
	// exactly and in one query.
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)*2)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, args...)

	rows, err := s.db.Query(`
		SELECT ca.name, cb.name, r.note
		FROM relations r
		JOIN characters ca ON ca.id = r.a_id
		JOIN characters cb ON cb.id = r.b_id
		WHERE r.a_id IN (`+marks+`) AND r.b_id IN (`+marks+`)
		ORDER BY ca.name, cb.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chars.Relation
	for rows.Next() {
		var r chars.Relation
		if err := rows.Scan(&r.A, &r.B, &r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
