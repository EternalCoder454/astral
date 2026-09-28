package store

import (
	"strings"
	"time"
)

// A scene's long-term memory: finding the moments from earlier in a scene that
// matter to what is happening now.
//
// A long scene keeps its recent turns word for word and folds everything older
// into a written recap. The recap keeps the plot. What it drops is the detail a
// reader notices when it comes back: the name of the ship, the exact thing she
// promised, the scar he said he got from his father. Those are still in the
// database. This finds them again when the conversation touches them, and puts
// the few that matter in front of the model beside the recap.
//
// The messages are indexed for full-text search by triggers, so the index
// cannot drift from the transcript: an edited turn is re-indexed and a deleted
// one leaves nothing behind, whichever part of the app did it.

func (s *Store) migrateMemory() {
	s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		content, content = 'messages', content_rowid = 'id', tokenize = 'porter unicode61')`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts (rowid, content) VALUES (new.id, new.content);
	END`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
		INSERT INTO messages_fts (messages_fts, rowid, content) VALUES ('delete', old.id, old.content);
	END`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE OF content ON messages BEGIN
		INSERT INTO messages_fts (messages_fts, rowid, content) VALUES ('delete', old.id, old.content);
		INSERT INTO messages_fts (rowid, content) VALUES (new.id, new.content);
	END`)
	// A database that had messages before the index existed is indexed once.
	// Asked of the index's own row count rather than remembered, so a copy
	// of the database from before this version is caught however it arrives.
	var indexed, stored int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages_fts_docsize`).Scan(&indexed)
	s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&stored)
	if stored > 0 && indexed == 0 {
		s.db.Exec(`INSERT INTO messages_fts (messages_fts) VALUES ('rebuild')`)
	}
}

// Moment is one earlier message a search found.
type Moment struct {
	ID          int64
	Role        string
	CharacterID int64
	Content     string
	At          time.Time
}

// minMomentChars is how long a message has to be to be worth recalling. A
// one-line "I nod." matches every search for "nod" and tells the model nothing.
const minMomentChars = 60

// Moments finds messages in one chat, at or before a given id, that match a
// piece of text, best first.
//
// Only the part of the scene before upto, because that is the part the model
// can no longer see word for word. Recalling something still in the context
// would spend the room twice on the same words.
func (s *Store) Moments(chatID, upto int64, text string, limit int) ([]Moment, error) {
	q := FTSQuery(text)
	if q == "" || upto <= 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 4
	}
	rows, err := s.db.Query(`
		SELECT m.id, m.role, m.character_id, m.content, m.created_at
		FROM messages_fts f
		JOIN messages m ON m.id = f.rowid
		WHERE messages_fts MATCH ? AND m.chat_id = ? AND m.id <= ?
		  AND length(m.content) >= ?
		ORDER BY bm25(messages_fts)
		LIMIT ?`, q, chatID, upto, minMomentChars, limit)
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
