package store

import "time"

// Your versions of Astral's prompts, written by hand or by the Prompt
// Optimizer. Kept in the database rather than the settings file because they
// are work: a prompt tuned over an evening is as much yours as a character is,
// and the database is what gets backed up.

// PromptOverrides returns every rewritten prompt, by prompt id.
func (s *Store) PromptOverrides() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id, text FROM prompt_overrides`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, err
		}
		out[id] = text
	}
	return out, rows.Err()
}

// SetPromptOverride saves your version of a prompt.
func (s *Store) SetPromptOverride(id, text string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`INSERT INTO prompt_overrides (id, text, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET text = excluded.text, updated_at = excluded.updated_at`,
		id, text, time.Now().Unix())
	return err
}

// DeletePromptOverride puts Astral's own version of a prompt back.
func (s *Store) DeletePromptOverride(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM prompt_overrides WHERE id = ?`, id)
	return err
}
