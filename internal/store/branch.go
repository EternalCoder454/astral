package store

import (
	"fmt"
	"strings"
	"time"
)

// Pinning and branching: two things every other roleplay app lets you do with
// a conversation and Astral did not.
//
// A pin is a message you never want the scene to forget. A long scene folds its
// early turns into a recap, and a recap keeps the plot and drops the exact
// words; a pinned turn is sent word for word however far back it is.
//
// A branch is a new chat that starts as a copy of this one up to a message, so
// a scene can go two ways without losing either.

func (s *Store) migratePins() {
	s.db.Exec(`ALTER TABLE messages ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_pinned ON messages(chat_id) WHERE pinned = 1`)
}

// SetMessagePinned pins or unpins one message.
func (s *Store) SetMessagePinned(id int64, pinned bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	v := 0
	if pinned {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE messages SET pinned = ? WHERE id = ?`, v, id)
	return err
}

// Pinned is every pinned message in a chat at or before upto, oldest first.
// Zero upto means the whole chat.
func (s *Store) Pinned(chatID, upto int64) ([]Moment, error) {
	if upto <= 0 {
		upto = 1<<62 - 1
	}
	rows, err := s.db.Query(`
		SELECT id, role, character_id, content, created_at
		FROM messages WHERE chat_id = ? AND pinned = 1 AND id <= ?
		ORDER BY id`, chatID, upto)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Moment
	for rows.Next() {
		var m Moment
		var at int64
		if err := rows.Scan(&m.ID, &m.Role, &m.CharacterID, &m.Content, &at); err != nil {
			return nil, err
		}
		m.At = fromUnix(at)
		m.Content = strings.TrimSpace(m.Content)
		out = append(out, m)
	}
	return out, rows.Err()
}

// BranchChat makes a new chat that is a copy of this one up to and including
// one message: the same character or cast, model, persona, direction and
// style, and every turn up to that point with its versions and pins.
//
// The recap comes too when it covers only turns being copied. When it reaches
// past the branch point it describes a future the branch does not have, so the
// branch starts without one and makes its own when it needs it.
func (s *Store) BranchChat(chatID, uptoID int64, title string) (Chat, error) {
	src, err := s.Chat(chatID)
	if err != nil {
		return Chat{}, err
	}
	msgs, err := s.Messages(chatID)
	if err != nil {
		return Chat{}, err
	}
	cut := -1
	for i, m := range msgs {
		if m.ID == uptoID {
			cut = i
			break
		}
	}
	if cut < 0 {
		return Chat{}, fmt.Errorf("message %d is not in chat %d", uptoID, chatID)
	}
	msgs = msgs[:cut+1]

	pinned := map[int64]bool{}
	if pins, err := s.Pinned(chatID, uptoID); err == nil {
		for _, p := range pins {
			pinned[p.ID] = true
		}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Chat{}, err
	}
	defer tx.Rollback()

	now := time.Now()
	res, err := tx.Exec(`
		INSERT INTO chats (character_id, world_id, title, model, kind, style_name, note, persona_id,
		                   created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		src.CharacterID, src.WorldID, title, src.Model, src.Kind, src.StyleName, src.Note, src.PersonaID,
		unix(now), unix(now))
	if err != nil {
		return Chat{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Chat{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO chat_cast (chat_id, character_id, position)
		SELECT ?, character_id, position FROM chat_cast WHERE chat_id = ?`, id, chatID); err != nil {
		return Chat{}, err
	}

	// Old ids to new, for the recap's and the lorebook's bookmarks.
	newID := make(map[int64]int64, len(msgs))
	for _, m := range msgs {
		pin := 0
		if pinned[m.ID] {
			pin = 1
		}
		r, err := tx.Exec(`
			INSERT INTO messages (chat_id, role, content, thinking, character_id, eval_count, tok_per_sec,
			                      created_at, versions, version, pinned)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			id, m.Role, m.Content, m.Thinking, m.CharacterID, m.EvalCount, m.TokPerSec,
			unix(m.CreatedAt), encodeVersions(m.Versions), m.Version, pin)
		if err != nil {
			return Chat{}, err
		}
		nid, err := r.LastInsertId()
		if err != nil {
			return Chat{}, err
		}
		newID[m.ID] = nid
	}

	// The recap's bookmark is the last turn it covers. Carried over only when
	// that turn is one of the ones copied.
	if n, ok := newID[src.SummaryUpto]; ok && strings.TrimSpace(src.Summary) != "" {
		if _, err := tx.Exec(`UPDATE chats SET summary = ?, summary_upto = ? WHERE id = ?`,
			src.Summary, n, id); err != nil {
			return Chat{}, err
		}
	}
	// What the lorebook has already learned from is learned either way; the
	// branch carries on from the same point, or from its end if that is
	// earlier.
	if src.LoreUpto > 0 {
		upto := int64(0)
		for old, n := range newID {
			if old <= src.LoreUpto && n > upto {
				upto = n
			}
		}
		if _, err := tx.Exec(`UPDATE chats SET lore_upto = ? WHERE id = ?`, upto, id); err != nil {
			return Chat{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Chat{}, err
	}
	return s.Chat(id)
}

// BranchTitle names a branch after the chat it came from.
func BranchTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Branch"
	}
	// A branch of a branch is still just a branch.
	title = strings.TrimSuffix(title, " (Branch)")
	return title + " (Branch)"
}
