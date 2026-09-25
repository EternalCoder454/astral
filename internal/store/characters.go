package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"astral/internal/chars"
)

// characterColumns is the select list, kept in one place so every scan agrees
// with every query.
const characterColumns = `id, name, description, personality, scenario, first_mes,
	mes_example, instructions, alt_greetings, creator, notes,
	version, tags, avatar_path, portrait_path, world_id, accent, created_at, updated_at`

// scanCharacter reads one row in characterColumns order.
func scanCharacter(sc interface{ Scan(...any) error }) (chars.Character, error) {
	var c chars.Character
	var altJSON, tagsJSON string
	var created, updated int64
	err := sc.Scan(&c.ID, &c.Name, &c.Description, &c.Personality, &c.Scenario,
		&c.FirstMes, &c.MesExample, &c.Instructions, &altJSON,
		&c.Creator, &c.Notes, &c.Version, &tagsJSON, &c.AvatarPath, &c.PortraitPath, &c.WorldID, &c.Accent,
		&created, &updated)
	if err != nil {
		return c, err
	}
	c.AltGreetings = decodeList(altJSON)
	c.Tags = decodeList(tagsJSON)
	c.CreatedAt, c.UpdatedAt = fromUnix(created), fromUnix(updated)
	return c, nil
}

// Lists are stored as JSON in a TEXT column. A malformed value decodes to nil
// rather than failing the read: a character with no tags is a far better
// outcome than a character that cannot be opened.
func encodeList(v []string) string {
	if len(v) == 0 {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func decodeList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// Characters returns every character, newest first.
func (s *Store) Characters() ([]chars.Character, error) {
	rows, err := s.db.Query(`SELECT ` + characterColumns + ` FROM characters ORDER BY updated_at DESC, id DESC`)
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

// Character returns one character by id.
func (s *Store) Character(id int64) (chars.Character, error) {
	row := s.db.QueryRow(`SELECT `+characterColumns+` FROM characters WHERE id = ?`, id)
	c, err := scanCharacter(row)
	if err == sql.ErrNoRows {
		return c, fmt.Errorf("no character with id %d", id)
	}
	return c, err
}

// CountCharacters returns how many characters exist.
func (s *Store) CountCharacters() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM characters`).Scan(&n)
	return n, err
}

// SaveCharacter inserts or updates a character and returns its id. A zero ID
// inserts; anything else updates in place.
func (s *Store) SaveCharacter(c chars.Character) (int64, error) {
	if strings.TrimSpace(c.Name) == "" {
		return 0, fmt.Errorf("a character needs a name")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now()
	if c.ID == 0 {
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		res, err := s.db.Exec(`
			INSERT INTO characters (name, description, personality, scenario, first_mes,
				mes_example, instructions, alt_greetings, creator, notes,
				version, tags, avatar_path, portrait_path, world_id, accent, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.Name, c.Description, c.Personality, c.Scenario, c.FirstMes,
			c.MesExample, c.Instructions, encodeList(c.AltGreetings),
			c.Creator, c.Notes, c.Version, encodeList(c.Tags), c.AvatarPath,
			c.PortraitPath, c.WorldID, c.Accent, unix(c.CreatedAt), unix(now))
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := s.db.Exec(`
		UPDATE characters SET name=?, description=?, personality=?, scenario=?,
			first_mes=?, mes_example=?, instructions=?,
			alt_greetings=?, creator=?, notes=?, version=?, tags=?, avatar_path=?,
			portrait_path=?, world_id=?, accent=?, updated_at=?
		WHERE id=?`,
		c.Name, c.Description, c.Personality, c.Scenario, c.FirstMes,
		c.MesExample, c.Instructions, encodeList(c.AltGreetings),
		c.Creator, c.Notes, c.Version, encodeList(c.Tags), c.AvatarPath,
		c.PortraitPath, c.WorldID, c.Accent, unix(now), c.ID)
	return c.ID, err
}

// DeleteCharacter removes a character. Chats that used it are kept — the
// transcript is yours, and losing a scene because you tidied up the cast is
// not a trade anyone would choose. Those chats report the character as gone.
func (s *Store) DeleteCharacter(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM characters WHERE id = ?`, id)
	return err
}
