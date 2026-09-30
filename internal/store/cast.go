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

	// When each member arrived, kept for those who stay: someone added now
	// arrives after the newest message, and was not there for anything
	// before it. The scene's own character was there from the start.
	// Somebody taken out and brought back arrives again, as though new: they
	// were there for what came before they left, but one arrival is all a
	// row keeps, and missing what they missed matters more than forgetting
	// what they saw.
	//
	// Read or nothing: a cast rewritten from a failed read would mark every
	// member as arriving now, and the scene would forget who was there.
	joined := map[int64]int64{}
	rows, err := tx.Query(`SELECT character_id, joined_after FROM chat_cast WHERE chat_id = ?`, chatID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, after int64
		if err := rows.Scan(&id, &after); err != nil {
			rows.Close()
			return err
		}
		joined[id] = after
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var founder, newest int64
	if err := tx.QueryRow(`SELECT character_id FROM chats WHERE id = ?`, chatID).Scan(&founder); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM messages WHERE chat_id = ?`, chatID).Scan(&newest); err != nil {
		return err
	}
	if _, ok := joined[founder]; !ok && founder != 0 {
		joined[founder] = 0
	}

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
		after, ok := joined[id]
		if !ok {
			after = newest
		}
		if _, err := tx.Exec(`
			INSERT INTO chat_cast (chat_id, character_id, position, joined_after) VALUES (?,?,?,?)`,
			chatID, id, pos, after); err != nil {
			return err
		}
		pos++
	}
	// The first member is also the chat's character, so everything that reads
	// chats.character_id, the sidebar's name and tint, the title, reopening a
	// scene, keeps working on a group without knowing groups exist.
	//
	// Except in a world. A scene set in a place has no character by design: its
	// narrator is the world itself, rebuilt from the world every time it is
	// opened rather than stored. Naming a cast member as the chat's character
	// there would reopen it tomorrow as that person's scene with the place gone.
	head := int64(0)
	for _, id := range ids {
		if id != 0 {
			head = id
			break
		}
	}
	if _, err := tx.Exec(`
		UPDATE chats
		SET character_id = CASE WHEN world_id = 0 THEN ? ELSE character_id END,
		    updated_at = ?
		WHERE id = ?`, head, unix(time.Now()), chatID); err != nil {
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

// CastJoins says, for the members of a scene's cast who arrived partway
// through, the id of the last message before they came. Members there from
// the start are not listed.
func (s *Store) CastJoins(chatID int64) (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT character_id, joined_after FROM chat_cast
		WHERE chat_id = ? AND joined_after > 0`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, after int64
		if err := rows.Scan(&id, &after); err != nil {
			return nil, err
		}
		out[id] = after
	}
	return out, rows.Err()
}

// FirstMessageAfter is the first message of a chat after the one with id
// after, which the model reads (not hidden, and not empty), or false when
// there is none.
func (s *Store) FirstMessageAfter(chatID, after int64) (Message, bool) {
	m := Message{ChatID: chatID}
	err := s.db.QueryRow(`SELECT id, role, content FROM messages
		WHERE chat_id = ? AND id > ? AND hidden = 0 AND TRIM(content) <> '' ORDER BY id LIMIT 1`, chatID, after).
		Scan(&m.ID, &m.Role, &m.Content)
	return m, err == nil
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
