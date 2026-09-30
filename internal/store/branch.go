package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"astral/internal/chars"
)

// Pinning and branching: two things every other roleplay app lets you do with
// a conversation and Astral did not.
//
// A pin is a message you never want the scene to forget. A long scene folds its
// early turns into a recap, and a recap keeps the plot and drops the exact
// words; a pinned turn is sent word for word however far back it is.
//
// A branch is a new chat that starts as a copy of this one up to a message, so
// a scene can go two ways without losing either.

func (s *Store) migratePins() {
	s.db.Exec(`ALTER TABLE messages ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_pinned ON messages(chat_id) WHERE pinned = 1`)
}

// SetMessagePinned pins or unpins one message.
func (s *Store) SetMessagePinned(id int64, pinned bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	v := 0
	if pinned {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE messages SET pinned = ? WHERE id = ?`, v, id)
	return err
}

// Pinned is every pinned message in a chat at or before upto, oldest first.
// Zero upto means the whole chat.
func (s *Store) Pinned(chatID, upto int64) ([]Moment, error) {
	if upto <= 0 {
		upto = 1<<62 - 1
	}
	rows, err := s.db.Query(`
		SELECT id, role, character_id, content, created_at
		FROM messages WHERE chat_id = ? AND pinned = 1 AND hidden = 0 AND id <= ?
		ORDER BY id`, chatID, upto)
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

// BranchChat makes a new chat that is a copy of this one up to and including
// one message: the same character or cast, model, persona, direction and
// style, and every turn up to that point with its versions and pins.
//
// The recap comes too when it covers only turns being copied. When it reaches
// past the branch point it describes a future the branch does not have, so the
// branch starts without one and makes its own when it needs it.
func (s *Store) BranchChat(chatID, uptoID int64, title string) (Chat, error) {
	src, err := s.Chat(chatID)
	if err != nil {
		return Chat{}, err
	}
	msgs, err := s.Messages(chatID)
	if err != nil {
		return Chat{}, err
	}
	cut := -1
	for i, m := range msgs {
		if m.ID == uptoID {
			cut = i
			break
		}
	}
	if cut < 0 {
		return Chat{}, fmt.Errorf("message %d is not in chat %d", uptoID, chatID)
	}
	all := msgs
	msgs = msgs[:cut+1]

	pinned := map[int64]bool{}
	if pins, err := s.Pinned(chatID, uptoID); err == nil {
		for _, p := range pins {
			pinned[p.ID] = true
		}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Chat{}, err
	}
	defer tx.Rollback()

	// Where and when the scene is and how it stands describe its end, when
	// Astral keeps them. A branch from earlier in it starts without them
	// rather than with a moment it has not reached, and Astral fills them in
	// again as it goes. What you wrote there yourself is yours and goes with
	// it, since nothing would write it again.
	atEnd := cut == len(all)-1
	row := src
	if !atEnd && src.SettingAuto {
		row.Setting, row.State = "", chars.SceneState{}
	}
	id, err := copyChatRow(tx, row, title)
	if err != nil {
		return Chat{}, err
	}
	newID, err := copyMessages(tx, id, msgs, pinned)
	if err != nil {
		return Chat{}, err
	}

	// The recap covers everything up to its bookmark, so it goes with a branch
	// taken at or after that point. The bookmark is carried by position: the
	// last copied turn at or before it, or just before the first one, which
	// is where a continuation's bookmark sits, on no turn of its own.
	if strings.TrimSpace(src.Summary) != "" && src.SummaryUpto > 0 && src.SummaryUpto <= uptoID && len(msgs) > 0 {
		upto := newID[msgs[0].ID] - 1
		for _, m := range msgs {
			if m.ID <= src.SummaryUpto && newID[m.ID] > upto {
				upto = newID[m.ID]
			}
		}
		if _, err := tx.Exec(`UPDATE chats SET summary = ?, summary_upto = ? WHERE id = ?`,
			src.Summary, upto, id); err != nil {
			return Chat{}, err
		}
	}
	if err := carryLoreMark(tx, src, id, newID); err != nil {
		return Chat{}, err
	}
	if err := tx.Commit(); err != nil {
		return Chat{}, err
	}
	return s.Chat(id)
}

// ContinueChat starts a new chat that carries a scene on from where it is: the
// same cast, persona, style, direction and setting; recap, the record of the
// story before fromID; the moments pinned before it, still pinned; and every
// message from fromID on, word for word.
//
// A scene run for hours reads less well than one just begun, however good its
// record: the transcript fills the model's memory and the character's own
// description is a smaller share of what it reads each turn. A new chat that
// knows the story, the pins and the last few exchanges starts with a nearly
// empty memory and the thread still in hand. Kindroid's Chat Break is the
// same idea. The original is left as it was.
func (s *Store) ContinueChat(chatID, fromID int64, recap, title string) (Chat, error) {
	src, err := s.Chat(chatID)
	if err != nil {
		return Chat{}, err
	}
	msgs, err := s.Messages(chatID)
	if err != nil {
		return Chat{}, err
	}
	at := -1
	for i, m := range msgs {
		if m.ID == fromID {
			at = i
			break
		}
	}
	if at < 0 {
		return Chat{}, fmt.Errorf("message %d is not in chat %d", fromID, chatID)
	}
	// The pinned moments from before the carried messages go first, pinned,
	// so the new chat recalls them the way the old one did.
	var carried []Message
	pinned := map[int64]bool{}
	for _, m := range msgs[:at] {
		if m.Pinned && !m.Hidden {
			carried = append(carried, m)
			pinned[m.ID] = true
		}
	}
	for _, m := range msgs[at:] {
		if m.Pinned {
			pinned[m.ID] = true
		}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Chat{}, err
	}
	defer tx.Rollback()

	id, err := copyChatRow(tx, src, title)
	if err != nil {
		return Chat{}, err
	}
	newID, err := copyMessages(tx, id, append(carried, msgs[at:]...), pinned)
	if err != nil {
		return Chat{}, err
	}
	// The record and the pins stand for everything before the carried
	// messages. The bookmark sits just before the first of them, so the
	// transcript starts there and the pins are recalled rather than read.
	if strings.TrimSpace(recap) != "" || len(carried) > 0 {
		if _, err := tx.Exec(`UPDATE chats SET summary = ?, summary_upto = ? WHERE id = ?`,
			strings.TrimSpace(recap), newID[fromID]-1, id); err != nil {
			return Chat{}, err
		}
	}
	if err := carryLoreMark(tx, src, id, newID); err != nil {
		return Chat{}, err
	}
	if err := tx.Commit(); err != nil {
		return Chat{}, err
	}
	return s.Chat(id)
}

// copyChatRow makes a new chat with everything about src but its messages,
// its record and its lorebook bookmark, and the same cast.
func copyChatRow(tx *sql.Tx, src Chat, title string) (int64, error) {
	now := time.Now()
	res, err := tx.Exec(`
		INSERT INTO chats (character_id, world_id, title, model, kind, style_name, note, persona_id,
		                   setting, setting_auto, state, reply_length, write_first, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		src.CharacterID, src.WorldID, title, src.Model, src.Kind, src.StyleName, src.Note, src.PersonaID,
		src.Setting, boolInt(src.SettingAuto), encodeState(src.State), src.ReplyLength, src.WriteFirst, unix(now), unix(now))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
		INSERT INTO chat_cast (chat_id, character_id, position, joined_after)
		SELECT ?, character_id, position, 0 FROM chat_cast WHERE chat_id = ?`, id, src.ID); err != nil {
		// Who arrived when is not carried: it is kept as a message id, and
		// the copy's messages have new ones. A copy starts with everyone there.
		return 0, err
	}
	return id, nil
}

// copyMessages copies msgs into chat id, in order, pinning the ones in
// pinned, and says which new id each old one became.
func copyMessages(tx *sql.Tx, id int64, msgs []Message, pinned map[int64]bool) (map[int64]int64, error) {
	newID := make(map[int64]int64, len(msgs))
	for _, m := range msgs {
		r, err := tx.Exec(`
			INSERT INTO messages (chat_id, role, content, thinking, character_id, eval_count, tok_per_sec,
			                      created_at, versions, version, pinned, hidden)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, m.Role, m.Content, m.Thinking, m.CharacterID, m.EvalCount, m.TokPerSec,
			unix(m.CreatedAt), encodeVersions(m.Versions), m.Version, boolInt(pinned[m.ID]), boolInt(m.Hidden))
		if err != nil {
			return nil, err
		}
		nid, err := r.LastInsertId()
		if err != nil {
			return nil, err
		}
		newID[m.ID] = nid
		// The copy says exactly what the original did, so the original's vector
		// is still true of it and does not have to be made again. Matched on the
		// text as well, so a message that was rewritten on its way over does not
		// inherit a vector that describes the old words.
		if _, err := tx.Exec(`
			INSERT INTO message_vectors (message_id, model, vec)
			SELECT ?, v.model, v.vec FROM message_vectors v JOIN messages o ON o.id = v.message_id
			WHERE v.message_id = ? AND o.content = ?`, nid, m.ID, m.Content); err != nil {
			return nil, err
		}
	}
	return newID, nil
}

// carryLoreMark carries what the lorebook has already learned from: learned
// is learned either way, so the copy carries on from the same point, or from
// its end if that is earlier.
func carryLoreMark(tx *sql.Tx, src Chat, id int64, newID map[int64]int64) error {
	if src.LoreUpto <= 0 {
		return nil
	}
	upto := int64(0)
	for old, n := range newID {
		if old <= src.LoreUpto && n > upto {
			upto = n
		}
	}
	_, err := tx.Exec(`UPDATE chats SET lore_upto = ? WHERE id = ?`, upto, id)
	return err
}

// BranchTitle names a branch after the chat it came from.
func BranchTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Branch"
	}
	// A branch of a branch is still just a branch.
	title = strings.TrimSuffix(title, " (Branch)")
	return title + " (Branch)"
}

// ContinueTitle names a continuation after the chat it carries on: Part 2,
// and the next number for one that is already a part.
func ContinueTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Part 2"
	}
	if i := strings.LastIndex(title, ", Part "); i >= 0 {
		if n, err := strconv.Atoi(title[i+len(", Part "):]); err == nil && n > 0 {
			return title[:i] + ", Part " + strconv.Itoa(n+1)
		}
	}
	return title + ", Part 2"
}
