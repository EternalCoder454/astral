package store

// ChatsWith is every conversation a character appears in, newest first.
//
// Both ways they can appear: as the chat's own character, and as a member of a
// cast. A character page that listed only the first would miss every group scene
// they are in, which for somebody who is mostly played in groups is all of them.
func (s *Store) ChatsWith(characterID int64) ([]Chat, error) {
	if characterID == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT c.id, c.character_id, c.world_id, c.title, c.model, c.kind,
		       c.created_at, c.updated_at,
		       COALESCE(ch.name, ''), COALESCE(ch.accent, 0), COALESCE(n.count, 0),
		       COALESCE(cc.count, 0)
		FROM chats c
		LEFT JOIN characters ch ON ch.id = c.character_id
		LEFT JOIN (SELECT chat_id, COUNT(*) AS count FROM messages GROUP BY chat_id) n
		       ON n.chat_id = c.id
		LEFT JOIN (SELECT chat_id, COUNT(*) AS count FROM chat_cast GROUP BY chat_id) cc
		       ON cc.chat_id = c.id
		WHERE c.character_id = ?
		   OR EXISTS (SELECT 1 FROM chat_cast x WHERE x.chat_id = c.id AND x.character_id = ?)
		ORDER BY c.updated_at DESC, c.id DESC`, characterID, characterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var c Chat
		var created, updated int64
		if err := rows.Scan(&c.ID, &c.CharacterID, &c.WorldID, &c.Title, &c.Model, &c.Kind,
			&created, &updated, &c.CharacterName, &c.Accent, &c.MessageCount,
			&c.CastSize); err != nil {
			return nil, err
		}
		c.CreatedAt, c.UpdatedAt = fromUnix(created), fromUnix(updated)
		out = append(out, c)
	}
	return out, rows.Err()
}
