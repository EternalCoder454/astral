package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"astral/internal/chars"
)

// What a conversation is for. A chat without a character is not a degenerate
// roleplay, it is a different thing with its own framing, and the two that
// exist here earn their place: plain assistant talk, and a session whose whole
// purpose is to produce a character.
const (
	KindRoleplay  = "roleplay"
	KindAssistant = "assistant"
	KindDesigner  = "designer"
	// KindStyleDesigner is the same idea as KindDesigner, but its product is a
	// writing style rather than a character.
	KindStyleDesigner = "style"
	// KindWorldDesigner is the same again, and its product is a setting with a
	// first lorebook in it.
	KindWorldDesigner = "world"
	// KindPromptOptimizer is the same again, and its product is a better
	// prompt. Its note holds the id of the prompt it is about, or nothing when
	// the prompt was brought in the conversation itself.
	KindPromptOptimizer = "prompt"
	// KindPersonaDesigner is the Persona Creator, whose product is one of the
	// people you play as.
	KindPersonaDesigner = "persona"
)

// Chat is one conversation.
type Chat struct {
	ID          int64
	CharacterID int64
	// WorldID is set when the scene is in a world rather than with a
	// character: a world is a place, and you can be in one without anyone in
	// particular being there. Zero for every other kind of chat, including a
	// character's own, which takes its world from the character.
	WorldID int64
	Title   string
	Model   string
	Kind    string
	// Summary is the running record of everything compacted out of this
	// chat's context, and SummaryUpto is the last message id it covers.
	Summary     string
	SummaryUpto int64
	// LoreUpto is the last message the lorebook has been taught from, so a
	// scene reopened tomorrow does not learn today's turns a second time.
	LoreUpto int64
	// StyleName is the writing style the transcript was written under. When
	// the active style no longer matches it, the prompt says so, otherwise
	// the model reads a scene full of its own prose in the old style and
	// writes a continuation to match, whatever the new style asks for.
	StyleName string
	// Note is the direction for this scene: where you want it to go next.
	// Unlike a character's instructions, which are standing rules, this is
	// about the next few turns and is expected to be rewritten or cleared as
	// the scene moves.
	Note string
	// PersonaID is which of your personas you play as in this chat. Zero means
	// whichever is in use by default, which is what every chat from before
	// there were several of you has.
	PersonaID int64
	// Setting is where and when the scene is now, in a line: "her flat, two
	// in the morning, rain". Sent every turn, so a long scene does not lose
	// track of the room it is in. See extras.go.
	Setting string
	// SettingAuto says Astral keeps Setting up to date after each reply,
	// which it does until you write your own.
	SettingAuto bool
	// State is how the scene stands besides where it is; see
	// chars.SceneState. Kept up to date with Setting, under SettingAuto.
	State chars.SceneState
	// ReplyLength is how long a reply the scene asks for, "" for whatever
	// the style says; see chars.LengthBlock.
	ReplyLength string
	// Archived chats are kept but listed apart from the rest. Writing a
	// message of your own in one brings it back; see AddMessage.
	Archived  bool
	CreatedAt time.Time
	UpdatedAt time.Time

	// Filled in by Chats() for the sidebar, not stored.
	CharacterName string
	Accent        int
	// AvatarPath is the character's picture, shown beside the chat's title.
	AvatarPath   string
	MessageCount int
	// CastSize is how many characters are in the scene, for the scenes that
	// have more than one. Zero for every other conversation, so the sidebar can
	// treat zero and one as the same thing.
	CastSize int
}

// Message is one turn, as persisted.
type Message struct {
	ID       int64
	ChatID   int64
	Role     string
	Content  string
	Thinking string
	// CharacterID is who spoke, in a scene with more than one character. Zero
	// for your own turns, and zero in a two-hander where the chat already
	// records the only character there is.
	//
	// The content is stored without a name on it. The name belongs to the
	// character, so a renamed character renames their old lines too, and the
	// transcript holds prose rather than prose with a label glued to the front.
	CharacterID int64
	EvalCount   int
	TokPerSec   float64
	CreatedAt   time.Time

	// Versions are the replies written for this turn, the one showing
	// included, when it has been written more than once; Version is which of
	// them is showing. Empty for a turn written once, which is almost all of
	// them. Content and Thinking always hold the one showing, so everything
	// that reads a transcript reads it without knowing versions exist.
	Versions []Version
	Version  int
	// Pinned messages are sent word for word however long the scene grows.
	// See branch.go.
	Pinned bool
	// Hidden messages stay in the transcript and are never sent to the
	// model. See extras.go.
	Hidden bool
}

// Version is one of the replies written for the same turn. Writing a reply
// again keeps the one before it, so a better first attempt is not lost to a
// worse second one.
type Version struct {
	Content  string `json:"c"`
	Thinking string `json:"t,omitempty"`
}

// Chats returns every chat, most recently updated first, joined to its
// character so the sidebar can show the name and tint without a query per row.
func (s *Store) Chats() ([]Chat, error) {
	// The message count comes from one grouped pass rather than a correlated
	// subquery per row. The subquery version was re-counting a chat's messages
	// once for every chat in the list, so the cost grew with chats times
	// messages, and this runs on every sidebar refresh, twice a turn.
	rows, err := s.db.Query(`
		SELECT c.id, c.character_id, c.world_id, c.title, c.model, c.kind, c.created_at, c.updated_at,
		       c.archived, COALESCE(ch.name, ''), COALESCE(ch.accent, 0), COALESCE(n.count, 0),
		       COALESCE(cc.count, 0), COALESCE(ch.avatar_path, '')
		FROM chats c
		LEFT JOIN characters ch ON ch.id = c.character_id
		LEFT JOIN (SELECT chat_id, COUNT(*) AS count FROM messages GROUP BY chat_id) n
		       ON n.chat_id = c.id
		-- The cast size, in the same one grouped pass as the message count
		-- rather than a query per row.
		LEFT JOIN (SELECT chat_id, COUNT(*) AS count FROM chat_cast GROUP BY chat_id) cc
		       ON cc.chat_id = c.id
		ORDER BY c.updated_at DESC, c.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var c Chat
		var created, updated int64
		if err := rows.Scan(&c.ID, &c.CharacterID, &c.WorldID, &c.Title, &c.Model, &c.Kind,
			&created, &updated, &c.Archived, &c.CharacterName, &c.Accent, &c.MessageCount,
			&c.CastSize, &c.AvatarPath); err != nil {
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
	var state string
	err := s.db.QueryRow(`
		SELECT c.id, c.character_id, c.world_id, c.title, c.model, c.kind, c.summary, c.summary_upto,
		       c.lore_upto, c.style_name, c.note, c.persona_id, c.setting, c.setting_auto, c.state, c.reply_length, c.archived, c.created_at, c.updated_at,
		       COALESCE(ch.name, ''), COALESCE(ch.accent, 0)
		FROM chats c
		LEFT JOIN characters ch ON ch.id = c.character_id
		WHERE c.id = ?`, id).
		Scan(&c.ID, &c.CharacterID, &c.WorldID, &c.Title, &c.Model, &c.Kind, &c.Summary, &c.SummaryUpto,
			&c.LoreUpto, &c.StyleName, &c.Note, &c.PersonaID, &c.Setting, &c.SettingAuto, &state, &c.ReplyLength, &c.Archived, &created, &updated, &c.CharacterName, &c.Accent)
	if err == sql.ErrNoRows {
		return c, fmt.Errorf("no chat with id %d", id)
	}
	if err != nil {
		return c, err
	}
	c.CreatedAt, c.UpdatedAt = fromUnix(created), fromUnix(updated)
	c.State = decodeState(state)
	return c, nil
}

// NewChat creates a conversation and returns it.
func (s *Store) NewChat(characterID int64, title, model, kind string) (Chat, error) {
	return s.NewChatIn(characterID, 0, title, model, kind)
}

// NewChatIn creates a conversation set in a world, with or without a character.
func (s *Store) NewChatIn(characterID, worldID int64, title, model, kind string) (Chat, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if kind == "" {
		kind = KindRoleplay
	}
	now := time.Now()
	res, err := s.db.Exec(`
		INSERT INTO chats (character_id, world_id, title, model, kind, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)`, characterID, worldID, title, model, kind, unix(now), unix(now))
	if err != nil {
		return Chat{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Chat{}, err
	}
	return Chat{
		ID: id, CharacterID: characterID, WorldID: worldID, Title: title, Model: model, Kind: kind,
		SettingAuto: true, CreatedAt: now, UpdatedAt: now,
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

// SetChatNote stores the direction for a scene.
func (s *Store) SetChatNote(id int64, note string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET note = ? WHERE id = ?`, note, id)
	return err
}

// SetChatStyle records the writing style a scene is being written under, so a
// later change to it can be noticed and announced to the model.
func (s *Store) SetChatStyle(id int64, name string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`UPDATE chats SET style_name = ? WHERE id = ?`, name, id)
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
		SELECT id, chat_id, role, content, thinking, character_id, eval_count, tok_per_sec, created_at,
		       versions, version, pinned, hidden
		FROM messages WHERE chat_id = ? AND id > ? ORDER BY id`, chatID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created int64
		var versions string
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &m.Thinking,
			&m.CharacterID, &m.EvalCount, &m.TokPerSec, &created, &versions, &m.Version, &m.Pinned, &m.Hidden); err != nil {
			return nil, err
		}
		m.CreatedAt = fromUnix(created)
		m.Versions = decodeVersions(versions, m.Version)
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMessage appends a turn and bumps the chat's updated_at, so the sidebar
// reorders. Both statements run in one transaction: a message that exists in a
// chat whose timestamp says otherwise would sort to the bottom of the list and
// look lost.
//
// A turn of your own also unarchives the chat. Here rather than in each place
// that sends, because the desktop and the phone both end up in this function,
// and a chat you are writing in belongs in the main list whichever of them you
// wrote from. A reply does not: it only ever follows one of yours, and a
// finished chat is not called back by a late one.
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

	versions := encodeVersions(m.Versions)
	if versions == "" {
		m.Version = 0
	}
	res, err := tx.Exec(`
		INSERT INTO messages (chat_id, role, content, thinking, character_id, eval_count, tok_per_sec, created_at,
		                      versions, version)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		m.ChatID, m.Role, m.Content, m.Thinking, m.CharacterID,
		m.EvalCount, m.TokPerSec, unix(m.CreatedAt), versions, m.Version)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	touch := `UPDATE chats SET updated_at = ? WHERE id = ?`
	if m.Role == "user" {
		touch = `UPDATE chats SET updated_at = ?, archived = 0 WHERE id = ?`
	}
	if _, err := tx.Exec(touch, unix(m.CreatedAt), m.ChatID); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// SetMessageContent rewrites a turn.
//
// The transcript is the prompt, so correcting a line is how a scene is steered
// back: by turn twenty the model's own replies are the strongest instruction
// in its context, and one wrong line left in place is imitated rather than
// forgotten. Deleting and rerolling throws away everything that was right
// about it.
//
// A turn with several versions has the one showing rewritten, so flipping away
// from an edited version and back again finds the edit still there.
func (s *Store) SetMessageContent(id int64, content string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var raw string
	var at int
	if err := s.db.QueryRow(`SELECT versions, version FROM messages WHERE id = ?`, id).Scan(&raw, &at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if vs := decodeVersions(raw, at); len(vs) > 0 {
		vs[at].Content = content
		_, err := s.db.Exec(`UPDATE messages SET content = ?, versions = ? WHERE id = ?`,
			content, encodeVersions(vs), id)
		return err
	}
	_, err := s.db.Exec(`UPDATE messages SET content = ? WHERE id = ?`, content, id)
	return err
}

// SetMessageVersion shows another of a turn's versions, and returns it.
func (s *Store) SetMessageVersion(id int64, at int) (Version, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var raw string
	var now int
	if err := s.db.QueryRow(`SELECT versions, version FROM messages WHERE id = ?`, id).Scan(&raw, &now); err != nil {
		return Version{}, err
	}
	vs := decodeVersions(raw, now)
	if at < 0 || at >= len(vs) {
		return Version{}, fmt.Errorf("there is no version %d of that reply", at+1)
	}
	v := vs[at]
	_, err := s.db.Exec(`UPDATE messages SET content = ?, thinking = ?, version = ? WHERE id = ?`,
		v.Content, v.Thinking, at, id)
	return v, err
}

// encodeVersions stores a turn's versions, or nothing for a turn with one.
func encodeVersions(vs []Version) string {
	if len(vs) < 2 {
		return ""
	}
	b, err := json.Marshal(vs)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeVersions reads them back. Anything unreadable, or a showing version
// that does not exist, reads as a turn with one version: the content column
// still holds the reply, so nothing that matters is lost.
func decodeVersions(raw string, at int) []Version {
	if raw == "" {
		return nil
	}
	var vs []Version
	if err := json.Unmarshal([]byte(raw), &vs); err != nil || len(vs) < 2 || at < 0 || at >= len(vs) {
		return nil
	}
	return vs
}

// DeleteMessage removes a single turn.
func (s *Store) DeleteMessage(id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM messages WHERE id = ?`, id)
	return err
}

// DeleteMessageIn removes one turn, but only if it belongs to the chat given.
//
// The pair is the point. A caller that knows a message id alone can delete any
// turn in any scene; a caller that has to name the scene as well can only
// delete what it was looking at.
func (s *Store) DeleteMessageIn(chatID, id int64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM messages WHERE id = ? AND chat_id = ?`, id, chatID)
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
			return "New Chat"
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
