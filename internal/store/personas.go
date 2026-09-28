package store

import (
	"database/sql"
	"fmt"
	"time"

	"astral/internal/chars"
)

// The people you play as. See chars.Profile.

func (s *Store) migratePersonas() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS personas (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT    NOT NULL DEFAULT '',
		age         TEXT    NOT NULL DEFAULT '',
		gender      TEXT    NOT NULL DEFAULT '',
		race        TEXT    NOT NULL DEFAULT '',
		appearance  TEXT    NOT NULL DEFAULT '',
		personality TEXT    NOT NULL DEFAULT '',
		background  TEXT    NOT NULL DEFAULT '',
		details     TEXT    NOT NULL DEFAULT '',
		accent      INTEGER NOT NULL DEFAULT 0,
		created_at  INTEGER NOT NULL DEFAULT 0,
		updated_at  INTEGER NOT NULL DEFAULT 0
	)`)
	s.db.Exec(`ALTER TABLE chats ADD COLUMN persona_id INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`ALTER TABLE personas ADD COLUMN avatar_path TEXT NOT NULL DEFAULT ''`)
}

const personaColumns = `id, name, age, gender, race, appearance, personality, background, details, accent, avatar_path`

func scanPersona(row interface{ Scan(...any) error }) (chars.Profile, error) {
	var p chars.Profile
	err := row.Scan(&p.ID, &p.Name, &p.Age, &p.Gender, &p.Race, &p.Appearance, &p.Personality,
		&p.Background, &p.Details, &p.Accent, &p.AvatarPath)
	return p, err
}

// Personas returns every persona, in the order they were made.
func (s *Store) Personas() ([]chars.Profile, error) {
	rows, err := s.db.Query(`SELECT ` + personaColumns + ` FROM personas ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chars.Profile
	for rows.Next() {
		p, err := scanPersona(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Persona returns one persona by id.
func (s *Store) Persona(id int64) (chars.Profile, error) {
	p, err := scanPersona(s.db.QueryRow(`SELECT `+personaColumns+` FROM personas WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return p, fmt.Errorf("no persona with id %d", id)
	}
	return p, err
}

// SavePersona inserts a persona when its id is zero and updates it otherwise,
// and returns its id.
func (s *Store) SavePersona(p chars.Profile) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	now := unix(time.Now())
	if p.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO personas
			(name, age, gender, race, appearance, personality, background, details, accent, avatar_path,
			 created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.Name, p.Age, p.Gender, p.Race, p.Appearance, p.Personality, p.Background, p.Details,
			p.Accent, p.AvatarPath, now, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := s.db.Exec(`UPDATE personas SET name = ?, age = ?, gender = ?, race = ?, appearance = ?,
		personality = ?, background = ?, details = ?, accent = ?, avatar_path = ?, updated_at = ?
		WHERE id = ?`,
		p.Name, p.Age, p.Gender, p.Race, p.Appearance, p.Personality, p.Background, p.Details,
		p.Accent, p.AvatarPath, now, p.ID)
	return p.ID, err
}

// DeletePersona removes a persona. Chats that were played as it go back to
// whichever persona is in use by default.
func (s *Store) DeletePersona(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.db.Exec(`UPDATE chats SET persona_id = 0 WHERE persona_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM personas WHERE id = ?`, id)
	return err
}

// SetChatPersona records which persona a chat is played as.
func (s *Store) SetChatPersona(chatID, personaID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET persona_id = ? WHERE id = ?`, personaID, chatID)
	return err
}

// AdoptOldChats gives every chat that has no persona this one. Run once, when
// the persona you had before there could be several becomes the first of them,
// so those scenes go on being played as who they were started as.
func (s *Store) AdoptOldChats(personaID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET persona_id = ? WHERE persona_id = 0`, personaID)
	return err
}
