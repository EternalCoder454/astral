package store

import (
	"time"

	"astral/internal/chars"
)

// MaxCast is how many characters can share one scene.
//
// Five, because the limit is the model's rather than the schema's. Every member
// costs a description and a personality in every prompt, and past four or five
// a small model stops telling them apart: two of them merge into one voice, or
// the reply becomes a roll call where each one says a line in order. A cap that
// is lower than the point where it falls apart is worth more than a cap that
// lets you build something that does not work.
const MaxCast = 5

// SetCast records which characters share a scene, in the order given. The
// order is the order they are introduced in the prompt, and it is the order the
// cast is listed in, so it is worth keeping.
//
// Replaces the whole cast rather than adding to it: a scene's cast is edited as
// a set, and a diff would be more code to get subtly wrong.
func (s *Store) SetCast(chatID int64, ids []int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after a successful Commit

	if _, err := tx.Exec(`DELETE FROM chat_cast WHERE chat_id = ?`, chatID); err != nil {
		return err
	}
	seen := make(map[int64]bool, len(ids))
	pos := 0
	for _, id := range ids {
		if id == 0 || seen[id] || pos >= MaxCast {
			continue
		}
		seen[id] = true
		if _, err := tx.Exec(`
			INSERT INTO chat_cast (chat_id, character_id, position) VALUES (?,?,?)`,
			chatID, id, pos); err != nil {
			return err
		}
		pos++
	}
	// The first member is also the chat's character, so everything that reads
	// chats.character_id — the sidebar's name and tint, the title, reopening a
	// scene — keeps working on a group without knowing groups exist.
	head := int64(0)
	for _, id := range ids {
		if id != 0 {
			head = id
			break
		}
	}
	if _, err := tx.Exec(`UPDATE chats SET character_id = ?, updated_at = ? WHERE id = ?`,
		head, unix(time.Now()), chatID); err != nil {
		return err
	}
	return tx.Commit()
}

// Cast returns the characters in a scene, in cast order.
//
// Empty for an ordinary two-hander: a scene with one character records no cast,
// so the caller can treat "no cast" and "not a group" as the same thing.
func (s *Store) Cast(chatID int64) ([]chars.Character, error) {
	rows, err := s.db.Query(`
		SELECT `+characterColumns+`
		FROM chat_cast cc
		JOIN characters ON characters.id = cc.character_id
		WHERE cc.chat_id = ?
		ORDER BY cc.position, cc.character_id`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chars.Character
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CastCounts returns how many characters each chat has, for the chats that have
// a cast at all.
//
// One query for the whole sidebar. The alternative was a cast read per row,
// which is the same mistake the message count used to make.
func (s *Store) CastCounts() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT chat_id, COUNT(*) FROM chat_cast GROUP BY chat_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int)
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// SpeakersIn is every character who has said something in a scene, whether or
// not they are still in its cast.
//
// The cast is who can speak next; this is who has spoken. They stop being the
// same list the moment somebody is written out of a scene, and the difference
// matters: their old lines are still in the transcript, and without a name to
// resolve they would be re-rendered and re-sent under whoever happens to be
// first in the cast now.
func (s *Store) SpeakersIn(chatID int64) ([]chars.Character, error) {
	rows, err := s.db.Query(`
		SELECT `+characterColumns+`
		FROM characters
		WHERE id IN (
			SELECT DISTINCT character_id FROM messages
			WHERE chat_id = ? AND character_id <> 0
		)`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chars.Character
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AttributeUnclaimed puts a name on the replies in a scene that have none, and
// returns how many it changed.
//
// This is what makes a two-hander into a group without losing its history. Its
// existing replies carry no speaker, because there was only one person it could
// have been; once a second character is in the room those lines need the name
// they always implied, or the transcript goes to the model half labelled and
// teaches it that labels are optional.
func (s *Store) AttributeUnclaimed(chatID, characterID int64) (int64, error) {
	if characterID == 0 {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.db.Exec(`
		UPDATE messages SET character_id = ?
		WHERE chat_id = ? AND character_id = 0 AND role = 'assistant'`, characterID, chatID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
