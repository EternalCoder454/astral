package store

import (
	"fmt"
	"strings"
)

// Vectors for a scene's messages, so an earlier moment can be found by what it
// is about as well as by the words in it.
//
// A word search misses a moment that says the same thing in other words: the
// person asks which boat leaves at sunrise, and the promise was about the
// Gannet sailing at dawn. An embedding model puts the two near each other. Each
// message that could be recalled gets one vector, made in the background, and
// recall compares the latest exchange with them.
//
// Like the full-text index, the table is kept honest by triggers, so a message
// that is edited or deleted, by whichever part of the app does it, leaves no
// vector behind that describes words it no longer holds.

func (s *Store) migrateMemoryVectors() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS message_vectors (
		message_id INTEGER PRIMARY KEY,
		model      TEXT    NOT NULL,
		vec        BLOB    NOT NULL
	)`)
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS message_vectors_delete AFTER DELETE ON messages BEGIN
		DELETE FROM message_vectors WHERE message_id = old.id;
	END`)
	// Only when the words changed: showing another version that says the same
	// thing, or saving an edit that changed nothing, leaves the vector true.
	s.db.Exec(`CREATE TRIGGER IF NOT EXISTS message_vectors_update AFTER UPDATE OF content ON messages
	WHEN old.content <> new.content BEGIN
		DELETE FROM message_vectors WHERE message_id = old.id;
	END`)
}

// MessageVector is one message's embedding.
type MessageVector struct {
	ID  int64
	Vec []float32
}

// PendingMessage is a message waiting to be embedded.
type PendingMessage struct {
	ID      int64
	Content string
}

// HasMessageVectors reports whether any message of a chat has a vector, of any
// model. It is what lets recall skip the embedding model entirely for a scene
// that has never had one.
func (s *Store) HasMessageVectors(chatID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`
		SELECT EXISTS (SELECT 1 FROM messages m JOIN message_vectors v ON v.message_id = m.id
					   WHERE m.chat_id = ?)`, chatID).Scan(&n)
	return n == 1, err
}

// MessageVectors lists the vectors, made with one model, of the messages of a
// chat at or before a given id.
//
// Only the messages Moments would consider, for the same reasons: the part of
// the scene before upto is the part the model can no longer see, and a hidden
// or one-line message is not worth recalling.
func (s *Store) MessageVectors(chatID, upto int64, model string) ([]MessageVector, error) {
	if upto <= 0 || model == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT v.message_id, v.vec
		FROM message_vectors v JOIN messages m ON m.id = v.message_id
		WHERE m.chat_id = ? AND m.id <= ? AND v.model = ?
		  AND length(m.content) >= ? AND m.hidden = 0
		ORDER BY m.id`, chatID, upto, model, minMomentChars)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageVector
	for rows.Next() {
		var v MessageVector
		var blob []byte
		if err := rows.Scan(&v.ID, &blob); err != nil {
			return nil, err
		}
		v.Vec = decodeVector(blob)
		out = append(out, v)
	}
	return out, rows.Err()
}

// MessagesWithoutVector lists the messages of a chat that are worth a vector
// and do not have one from this model, oldest first. A message with a vector
// from some other model is listed: those vectors are in a different space and
// are of no use to this one.
func (s *Store) MessagesWithoutVector(chatID int64, model string, limit int) ([]PendingMessage, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.db.Query(`
		SELECT m.id, m.content FROM messages m
		LEFT JOIN message_vectors v ON v.message_id = m.id AND v.model = ?
		WHERE m.chat_id = ? AND v.message_id IS NULL
		  AND length(m.content) >= ? AND m.hidden = 0
		ORDER BY m.id LIMIT ?`, model, chatID, minMomentChars, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingMessage
	for rows.Next() {
		var p PendingMessage
		if err := rows.Scan(&p.ID, &p.Content); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveMessageVectors stores the embeddings of a batch of messages, vecs[i]
// being the vector of from[i].
//
// A vector is kept only while its message still says what it was made from.
// Embedding takes a moment, and a message edited or deleted meanwhile must not
// end up with a vector that describes the words it used to have.
func (s *Store) SaveMessageVectors(model string, from []PendingMessage, vecs [][]float32) error {
	if len(from) != len(vecs) {
		return fmt.Errorf("%d messages and %d vectors", len(from), len(vecs))
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, p := range from {
		if len(vecs[i]) == 0 {
			continue
		}
		if _, err := tx.Exec(`
			INSERT OR REPLACE INTO message_vectors (message_id, model, vec)
			SELECT id, ?, ? FROM messages WHERE id = ? AND content = ?`,
			model, encodeVector(vecs[i]), p.ID, p.Content); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MomentsByID reads the given messages of a chat as moments, in the order the
// ids were given. One that is gone, hidden or too short to recall is left out.
func (s *Store) MomentsByID(chatID int64, ids []int64) ([]Moment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{chatID, minMomentChars}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.Query(`
		SELECT id, role, character_id, content, created_at FROM messages
		WHERE chat_id = ? AND hidden = 0 AND length(content) >= ? AND id IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found, err := scanMoments(rows)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Moment, len(found))
	for _, m := range found {
		byID[m.ID] = m
	}
	out := make([]Moment, 0, len(found))
	for _, id := range ids {
		if m, ok := byID[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// Cosine is the similarity of two vectors, 1 for the same direction, and -1
// for vectors that cannot be compared.
func Cosine(a, b []float32) float64 { return cosine(a, b) }
