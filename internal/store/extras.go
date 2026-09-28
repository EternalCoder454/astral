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
