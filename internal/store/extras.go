package store

import (
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
