package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// What a conversation is for. A chat without a character is not a degenerate
// roleplay — it is a different thing with its own framing, and the two that
// exist here earn their place: plain assistant talk, and a session whose whole
// purpose is to produce a character.
const (
	KindRoleplay  = "roleplay"
	KindAssistant = "assistant"
	KindDesigner  = "designer"
	// KindStyleDesigner is the same idea as KindDesigner, but its product is a
	// writing style rather than a character.
	KindStyleDesigner = "style"
)

// Chat is one conversation.
type Chat struct {
	ID          int64
	CharacterID int64
	Title       string
	Model       string
	Kind        string
	// Summary is the running record of everything compacted out of this
	// chat's context, and SummaryUpto is the last message id it covers.
	Summary     string
	SummaryUpto int64
	// LoreUpto is the last message the lorebook has been taught from, so a
	// scene reopened tomorrow does not learn today's turns a second time.
	LoreUpto  int64
	CreatedAt time.Time
	UpdatedAt time.Time

	// Filled in by Chats() for the sidebar, not stored.
	CharacterName string
	Accent        int
	MessageCount  int
}

// Message is one turn, as persisted.
type Message struct {
	ID        int64
	ChatID    int64
	Role      string
	Content   string
	Thinking  string
	EvalCount int
	TokPerSec float64
	CreatedAt time.Time
}

// Chats returns every chat, most recently updated first, joined to its
// character so the sidebar can show the name and tint without a query per row.
func (s *Store) Chats() ([]Chat, error) {
	// The message count comes from one grouped pass rather than a correlated
	// subquery per row. The subquery version was re-counting a chat's messages
	// once for every chat in the list, so the cost grew with chats times
	// messages — and this runs on every sidebar refresh, twice a turn.
	rows, err := s.db.Query(`
		SELECT c.id, c.character_id, c.title, c.model, c.kind, c.created_at, c.updated_at,
		       COALESCE(ch.name, ''), COALESCE(ch.accent, 0), COALESCE(n.count, 0)
		FROM chats c
		LEFT JOIN characters ch ON ch.id = c.character_id
		LEFT JOIN (SELECT chat_id, COUNT(*) AS count FROM messages GROUP BY chat_id) n
		       ON n.chat_id = c.id
		ORDER BY c.updated_at DESC, c.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var c Chat
		var created, updated int64
		if err := rows.Scan(&c.ID, &c.CharacterID, &c.Title, &c.Model, &c.Kind,
			&created, &updated, &c.CharacterName, &c.Accent, &c.MessageCount); err != nil {
			return nil, err
		}
		c.CreatedAt, c.UpdatedAt = fromUnix(created), fromUnix(updated)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Chat returns one chat by id.
func (s *Store) Chat(id int64) (Chat, error) {
	var c Chat
	var created, updated int64
	err := s.db.QueryRow(`
		SELECT c.id, c.character_id, c.title, c.model, c.kind, c.summary, c.summary_upto,
		       c.lore_upto, c.created_at, c.updated_at, COALESCE(ch.name, ''), COALESCE(ch.accent, 0)
		FROM chats c
		LEFT JOIN characters ch ON ch.id = c.character_id
		WHERE c.id = ?`, id).
		Scan(&c.ID, &c.CharacterID, &c.Title, &c.Model, &c.Kind, &c.Summary, &c.SummaryUpto,
			&c.LoreUpto, &created, &updated, &c.CharacterName, &c.Accent)
	if err == sql.ErrNoRows {
		return c, fmt.Errorf("no chat with id %d", id)
	}
	if err != nil {
		return c, err
	}
	c.CreatedAt, c.UpdatedAt = fromUnix(created), fromUnix(updated)
	return c, nil
}

// NewChat creates a conversation and returns it.
func (s *Store) NewChat(characterID int64, title, model, kind string) (Chat, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if kind == "" {
		kind = KindRoleplay
	}
	now := time.Now()
	res, err := s.db.Exec(`
		INSERT INTO chats (character_id, title, model, kind, created_at, updated_at)
		VALUES (?,?,?,?,?,?)`, characterID, title, model, kind, unix(now), unix(now))
	if err != nil {
		return Chat{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Chat{}, err
	}
	return Chat{
		ID: id, CharacterID: characterID, Title: title, Model: model, Kind: kind,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// RenameChat sets a chat's title.
func (s *Store) RenameChat(id int64, title string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET title = ?, updated_at = ? WHERE id = ?`,
		title, unix(time.Now()), id)
	return err
}

// SetChatModel records which model a conversation is using, so reopening it
// resumes with the same one rather than whatever is currently selected.
func (s *Store) SetChatModel(id int64, model string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET model = ? WHERE id = ?`, model, id)
	return err
}

// DeleteChat removes a chat and, by the schema's cascade, its messages.
func (s *Store) DeleteChat(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM chats WHERE id = ?`, id)
	return err
}

// SetChatSummary stores the running recap and how far it reaches.
func (s *Store) SetChatSummary(id int64, summary string, uptoID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET summary = ?, summary_upto = ? WHERE id = ?`,
		summary, uptoID, id)
	return err
}

// SetChatLoreUpto records how far the lorebook has been taught from a chat.
func (s *Store) SetChatLoreUpto(id, uptoID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET lore_upto = ? WHERE id = ?`, uptoID, id)
	return err
}

// MessagesAfter returns the turns newer than afterID, oldest first. A scene
// whose early turns have been folded into the recap only needs the rest, and
// on a long transcript that is most of the read avoided.
func (s *Store) MessagesAfter(chatID, afterID int64) ([]Message, error) {
	return s.messages(chatID, afterID)
}

// Messages returns a chat's turns, oldest first.
func (s *Store) Messages(chatID int64) ([]Message, error) {
	return s.messages(chatID, 0)
}

func (s *Store) messages(chatID, afterID int64) ([]Message, error) {
	rows, err := s.db.Query(`
		SELECT id, chat_id, role, content, thinking, eval_count, tok_per_sec, created_at
		FROM messages WHERE chat_id = ? AND id > ? ORDER BY id`, chatID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created int64
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &m.Thinking,
			&m.EvalCount, &m.TokPerSec, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = fromUnix(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMessage appends a turn and bumps the chat's updated_at, so the sidebar
// reorders. Both statements run in one transaction: a message that exists in a
// chat whose timestamp says otherwise would sort to the bottom of the list and
// look lost.
func (s *Store) AddMessage(m Message) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() // no-op after a successful Commit

	res, err := tx.Exec(`
		INSERT INTO messages (chat_id, role, content, thinking, eval_count, tok_per_sec, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		m.ChatID, m.Role, m.Content, m.Thinking, m.EvalCount, m.TokPerSec, unix(m.CreatedAt))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE chats SET updated_at = ? WHERE id = ?`,
		unix(m.CreatedAt), m.ChatID); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// DeleteMessage removes a single turn.
func (s *Store) DeleteMessage(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM messages WHERE id = ?`, id)
	return err
}

// DeleteMessagesFrom removes a turn and everything after it in the same chat.
// This is what a regenerate does: rewinding to a point in the scene means the
// turns that followed are no longer part of it.
func (s *Store) DeleteMessagesFrom(chatID, fromID int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM messages WHERE chat_id = ? AND id >= ?`, chatID, fromID)
	return err
}

// TitleFrom derives a chat title from its first user message: the first line,
// trimmed to something that fits a sidebar row.
func TitleFrom(text string) string {
	line := strings.TrimSpace(text)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	line = strings.Join(strings.Fields(line), " ")
	const max = 48
	if len(line) <= max {
		if line == "" {
			return "New chat"
		}
		return line
	}
	// Cut on a word boundary when there is one nearby, so titles do not end
	// mid-word for the sake of three extra characters.
	cut := line[:max]
	if i := strings.LastIndexByte(cut, ' '); i > max-14 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "…"
}
