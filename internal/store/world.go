package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"astral/internal/world"
)

// Worlds returns every world, newest touched first.
func (s *Store) Worlds() ([]world.World, error) {
	rows, err := s.db.Query(`
		SELECT id, name, description, rules, created_at, updated_at
		FROM worlds ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []world.World
	for rows.Next() {
		var w world.World
		var created, updated int64
		if err := rows.Scan(&w.ID, &w.Name, &w.Description, &w.Rules, &created, &updated); err != nil {
			return nil, err
		}
		w.CreatedAt, w.UpdatedAt = fromUnix(created), fromUnix(updated)
		out = append(out, w)
	}
	return out, rows.Err()
}

// World returns one world by id.
func (s *Store) World(id int64) (world.World, error) {
	var w world.World
	var created, updated int64
	err := s.db.QueryRow(`
		SELECT id, name, description, rules, created_at, updated_at FROM worlds WHERE id = ?`, id).
		Scan(&w.ID, &w.Name, &w.Description, &w.Rules, &created, &updated)
	if err == sql.ErrNoRows {
		return w, fmt.Errorf("no world with id %d", id)
	}
	if err != nil {
		return w, err
	}
	w.CreatedAt, w.UpdatedAt = fromUnix(created), fromUnix(updated)
	return w, nil
}

// SaveWorld inserts or updates a world and returns its id.
func (s *Store) SaveWorld(w world.World) (int64, error) {
	if strings.TrimSpace(w.Name) == "" {
		return 0, fmt.Errorf("a world needs a name")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now()
	if w.ID == 0 {
		res, err := s.db.Exec(`
			INSERT INTO worlds (name, description, rules, created_at, updated_at) VALUES (?,?,?,?,?)`,
			w.Name, w.Description, w.Rules, unix(now), unix(now))
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := s.db.Exec(`UPDATE worlds SET name=?, description=?, rules=?, updated_at=? WHERE id=?`,
		w.Name, w.Description, w.Rules, unix(now), w.ID)
	return w.ID, err
}

// DeleteWorld removes a world and, by the schema's cascade, its lore. The
// characters that belonged to it are kept and simply stop having a setting.
func (s *Store) DeleteWorld(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.db.Exec(`UPDATE characters SET world_id = 0 WHERE world_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM worlds WHERE id = ?`, id)
	return err
}

// LoreEntries returns a world's lorebook.
func (s *Store) LoreEntries(worldID int64) ([]world.Entry, error) {
	rows, err := s.db.Query(`
		SELECT id, world_id, name, "keys", content, enabled, constant, auto, priority,
		       confidence, created_at, updated_at
		FROM lore_entries WHERE world_id = ? ORDER BY priority DESC, id`, worldID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []world.Entry
	for rows.Next() {
		var e world.Entry
		var keysJSON string
		var created, updated int64
		if err := rows.Scan(&e.ID, &e.WorldID, &e.Name, &keysJSON, &e.Content,
			&e.Enabled, &e.Constant, &e.Auto, &e.Priority, &e.Confidence,
			&created, &updated); err != nil {
			return nil, err
		}
		e.Keys = decodeList(keysJSON)
		e.CreatedAt, e.UpdatedAt = fromUnix(created), fromUnix(updated)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SaveLoreEntry inserts or updates an entry.
//
// It upserts on (world_id, name) rather than on the id, because the model
// writing lore back does not know ids: it knows it has learned something more
// about "Kestrel Bay". The unique index is what makes re-learning a subject an
// update instead of a second entry.
//
// A hand-written entry is never overwritten by an automatic one. Someone who
// wrote lore themselves has made a decision, and a background process quietly
// replacing it would be the worst kind of surprise.
func (s *Store) SaveLoreEntry(e world.Entry) (int64, error) {
	name := strings.TrimSpace(e.Name)
	if name == "" {
		return 0, fmt.Errorf("a lore entry needs a name")
	}
	if e.WorldID == 0 {
		return 0, fmt.Errorf("a lore entry needs a world")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now()
	var existingID int64
	var existingAuto bool
	err := s.db.QueryRow(`SELECT id, auto FROM lore_entries WHERE world_id = ? AND name = ?`,
		e.WorldID, name).Scan(&existingID, &existingAuto)
	switch {
	case err == sql.ErrNoRows:
		res, err := s.db.Exec(`
			INSERT INTO lore_entries (world_id, name, "keys", content, enabled, constant, auto,
				priority, confidence, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			e.WorldID, name, encodeList(e.Keys), e.Content, e.Enabled, e.Constant, e.Auto,
			e.Priority, e.Confidence, unix(now), unix(now))
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	case err != nil:
		return 0, err
	}

	if e.Auto && !existingAuto {
		return existingID, ErrWouldOverwriteManual
	}
	_, err = s.db.Exec(`
		UPDATE lore_entries SET "keys"=?, content=?, enabled=?, constant=?, auto=?,
			priority=?, confidence=?, updated_at=? WHERE id=?`,
		encodeList(e.Keys), e.Content, e.Enabled, e.Constant, e.Auto, e.Priority,
		e.Confidence, unix(now), existingID)
	return existingID, err
}

// ErrWouldOverwriteManual is returned when an automatic update would replace
// an entry a person wrote.
var ErrWouldOverwriteManual = fmt.Errorf("entry was written by hand and will not be overwritten automatically")

// DeleteLoreEntry removes one entry.
func (s *Store) DeleteLoreEntry(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM lore_entries WHERE id = ?`, id)
	return err
}

// CountLore returns how many entries a world has.
func (s *Store) CountLore(worldID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM lore_entries WHERE world_id = ?`, worldID).Scan(&n)
	return n, err
}

// CountCharactersInWorld is how many characters live in a world. The home
// screen shows it because a world with nobody in it cannot be played, and that
// is worth knowing before you click into it.
func (s *Store) CountCharactersInWorld(worldID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM characters WHERE world_id = ?`, worldID).Scan(&n)
	return n, err
}
