package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"astral/internal/chars"
)

// Three small things other roleplay apps have: favorite characters, messages
// kept in the transcript but hidden from the model, and a scene's current
// setting in one line.

func (s *Store) migrateExtras() {
	s.db.Exec(`ALTER TABLE characters ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`ALTER TABLE messages ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`ALTER TABLE chats ADD COLUMN setting TEXT NOT NULL DEFAULT ''`)
	// Whether Astral keeps the setting up to date as the scene moves. On
	// until you write your own.
	s.db.Exec(`ALTER TABLE chats ADD COLUMN setting_auto INTEGER NOT NULL DEFAULT 1`)
	// A chat put away: out of the main list, kept, and back in it the next
	// time you write in it.
	s.db.Exec(`ALTER TABLE chats ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`)
	// How the scene stands besides where it is: see chars.SceneState. JSON,
	// so a part can be added without another column.
	s.db.Exec(`ALTER TABLE chats ADD COLUMN state TEXT NOT NULL DEFAULT ''`)
	// How long a reply the scene asks for; see chars.LengthBlock.
	s.db.Exec(`ALTER TABLE chats ADD COLUMN reply_length TEXT NOT NULL DEFAULT ''`)
	// When each member of a cast arrived: the last message before they came,
	// 0 for there from the start. See SetCast.
	s.db.Exec(`ALTER TABLE chat_cast ADD COLUMN joined_after INTEGER NOT NULL DEFAULT 0`)
	// When a lore entry is sent besides being mentioned: how often, how far
	// into the scene, and which entries it takes turns with. See world.Entry.
	// The defaults are an entry that is sent whenever it is triggered, so every
	// entry from before these existed behaves as it did.
	s.db.Exec(`ALTER TABLE lore_entries ADD COLUMN chance INTEGER NOT NULL DEFAULT 100`)
	s.db.Exec(`ALTER TABLE lore_entries ADD COLUMN wait INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`ALTER TABLE lore_entries ADD COLUMN group_name TEXT NOT NULL DEFAULT ''`)
	// Writes First: how many minutes of silence before the character writes
	// unprompted, 0 for never, and the id of the last message written that
	// way, so one silence is answered once. See scene.WriteFirstDue.
	s.db.Exec(`ALTER TABLE chats ADD COLUMN write_first INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`ALTER TABLE chats ADD COLUMN nudged_at INTEGER NOT NULL DEFAULT 0`)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SetFavorite marks a character as a favorite, or not.
func (s *Store) SetFavorite(id int64, favorite bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE characters SET favorite = ? WHERE id = ?`, boolInt(favorite), id)
	return err
}

// SetMessageHidden hides a message from the model, or shows it again. It
// stays in the transcript either way: hiding is for an out of character
// aside, or a turn that went wrong and should stop being precedent.
func (s *Store) SetMessageHidden(id int64, hidden bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE messages SET hidden = ? WHERE id = ?`, boolInt(hidden), id)
	return err
}

// SetChatArchived puts a chat away, or brings it back. It leaves updated_at
// alone: archiving is tidying, not activity, so a chat brought back sits where
// its last message put it rather than jumping to the top of the list.
func (s *Store) SetChatArchived(id int64, archived bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET archived = ? WHERE id = ?`, boolInt(archived), id)
	return err
}

// SetChatReplyLength records how long a reply the scene asks for.
func (s *Store) SetChatReplyLength(id int64, length string) error {
	switch length {
	case "", chars.LengthShort, chars.LengthMedium, chars.LengthLong:
	default:
		return fmt.Errorf("no reply length called %q", length)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET reply_length = ? WHERE id = ?`, length, id)
	return err
}

// MaxWriteFirst is the longest wait a scene can be set to, a week in minutes:
// past that a scene is finished rather than quiet.
const MaxWriteFirst = 7 * 24 * 60

// SetChatWriteFirst records how many minutes of silence pass before the
// scene's character writes first, 0 for never.
func (s *Store) SetChatWriteFirst(id int64, minutes int) error {
	if minutes < 0 || minutes > MaxWriteFirst {
		return fmt.Errorf("a scene cannot wait %d minutes to write first", minutes)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET write_first = ? WHERE id = ?`, minutes, id)
	return err
}

// SetChatNudged records the message the character wrote first, so the silence
// it broke is not answered a second time. It leaves updated_at alone, since
// AddMessage has just moved it.
func (s *Store) SetChatNudged(id, messageID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET nudged_at = ? WHERE id = ?`, messageID, id)
	return err
}

// LastMessage is the newest turn of a chat, and false when it has none. It is
// what a due check needs of it, who wrote it and when, so it reads no more
// than that.
func (s *Store) LastMessage(chatID int64) (Message, bool, error) {
	var m Message
	var created int64
	err := s.db.QueryRow(`
		SELECT id, chat_id, role, content, character_id, created_at
		FROM messages WHERE chat_id = ? ORDER BY id DESC LIMIT 1`, chatID).
		Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &m.CharacterID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	m.CreatedAt = fromUnix(created)
	return m, true, nil
}

// ChatsWritingFirst is the chats that are set to write first, and not put
// away: the candidates, which scene.WriteFirstDue then looks at one by one.
func (s *Store) ChatsWritingFirst() ([]Chat, error) {
	rows, err := s.db.Query(`SELECT id FROM chats WHERE write_first > 0 AND archived = 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Chat, 0, len(ids))
	for _, id := range ids {
		ch, err := s.Chat(id)
		if err != nil {
			continue // deleted since the list was read
		}
		out = append(out, ch)
	}
	return out, nil
}

// SetChatState records how a scene stands besides where it is.
func (s *Store) SetChatState(id int64, state chars.SceneState) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET state = ? WHERE id = ?`, encodeState(state), id)
	return err
}

// encodeState is a scene's state as stored: empty for none, so a scene
// without one costs nothing and reads the same as before there was one.
func encodeState(st chars.SceneState) string {
	if st.Empty() {
		return ""
	}
	b, err := json.Marshal(st)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeState reads a stored state; anything unreadable is no state.
func decodeState(s string) chars.SceneState {
	var st chars.SceneState
	if strings.TrimSpace(s) != "" {
		_ = json.Unmarshal([]byte(s), &st)
	}
	return st
}

// SetChatSettingAuto says whether Astral keeps a scene's setting up to date.
func (s *Store) SetChatSettingAuto(id int64, auto bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET setting_auto = ? WHERE id = ?`, boolInt(auto), id)
	return err
}

// SetChatSetting stores where and when a scene is now.
func (s *Store) SetChatSetting(id int64, setting string) error {
	setting = strings.TrimSpace(setting)
	if r := []rune(setting); len(r) > chars.SettingChars {
		setting = string(r[:chars.SettingChars])
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET setting = ? WHERE id = ?`, setting, id)
	return err
}
