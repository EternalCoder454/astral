package store

import (
	"strings"
	"unicode"
)

// Finding a conversation again.
//
// The sidebar lists chats by when they were last touched, which is the right
// order for picking up where you left off and the wrong one for "the scene
// where she finally told him about the Guild". Every message is already in a
// full-text index for a scene's own memory (see memory.go), so searching all
// of them costs a query, not a new index.

// ChatHit is a chat a search found. Snippet is the line that matched, with the
// matching words marked by \x01 and \x02, or empty when it was the title.
type ChatHit struct {
	ChatID  int64
	Snippet string
}

// SearchChats finds chats whose title or messages contain every word of the
// query, titles first and then the best message matches, one hit per chat.
func (s *Store) SearchChats(query string, limit int) ([]ChatHit, error) {
	words := searchWords(query)
	if len(words) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	seen := map[int64]bool{}
	var out []ChatHit

	// Titles: every word somewhere in the title, in any case.
	var where []string
	var args []any
	for _, w := range words {
		where = append(where, `lower(title) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(w)+"%")
	}
	rows, err := s.db.Query(`SELECT id FROM chats WHERE `+strings.Join(where, " AND ")+
		` ORDER BY updated_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, ChatHit{ChatID: id})
		}
	}
	rows.Close()

	// Messages: every word, each as the start of a word, so "cart" finds
	// "cartographer" as it is being typed.
	terms := make([]string, len(words))
	for i, w := range words {
		terms[i] = `"` + w + `"*`
	}
	rows, err = s.db.Query(`
		SELECT m.chat_id, snippet(messages_fts, 0, char(1), char(2), '…', 12)
		FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ?
		ORDER BY bm25(messages_fts)
		LIMIT ?`, strings.Join(terms, " "), limit*8)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() && len(out) < limit {
		var h ChatHit
		if err := rows.Scan(&h.ChatID, &h.Snippet); err != nil {
			return nil, err
		}
		if seen[h.ChatID] {
			continue
		}
		seen[h.ChatID] = true
		h.Snippet = strings.Join(strings.Fields(h.Snippet), " ")
		out = append(out, h)
	}
	return out, rows.Err()
}

// searchWords splits a query into the words worth searching for.
func searchWords(q string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(w)) >= 2 {
			out = append(out, w)
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
