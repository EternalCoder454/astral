package scene

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// Writes First: a scene can ask its character to speak up after you have gone
// quiet. It is the one thing a companion app does that a chat window cannot,
// which is start the conversation.
//
// The rule is deliberately small, so it is the same wherever it is asked: a
// scene is due when its last message is the character's and is old enough, and
// it is not due again until you have written. Everything here is derived from
// the store, so the desktop's timer and the tests use the same code.

// WriteFirstChoice is one wait a scene can be set to.
type WriteFirstChoice struct {
	Minutes int
	Label   string
}

// WriteFirstChoices are the waits offered, in the order they are shown. The
// first, 0, is off.
var WriteFirstChoices = []WriteFirstChoice{
	{0, "Off"},
	{10, "After 10 Minutes"},
	{60, "After an Hour"},
	{180, "After Three Hours"},
}

// WriteFirstWait is a wait in words, for "writes first after ...". The
// empty string for off.
func WriteFirstWait(minutes int) string {
	switch {
	case minutes <= 0:
		return ""
	case minutes == 1:
		return "a minute"
	case minutes < 60:
		return strconv.Itoa(minutes) + " minutes"
	case minutes == 60:
		return "an hour"
	case minutes%60 == 0:
		return strconv.Itoa(minutes/60) + " hours"
	}
	return strconv.Itoa(minutes) + " minutes"
}

// WriteFirstDue reports whether a scene's character should write first now.
//
// The scene has to be a roleplay with somebody in it, and set to write first.
// Its last stored message has to be the character's, since if it is yours
// there is a reply owed to you and it is not you who went quiet, and not the
// message that was itself written first, so a silence is answered once and
// the scene is due again only after you have written. And that message has to
// be at least the chosen wait old.
//
// last is the newest stored message of the chat, the zero Message for none.
func WriteFirstDue(ch store.Chat, cast []chars.Character, last store.Message, now time.Time) bool {
	if ch.WriteFirst <= 0 || ch.Archived || !CanDraft(ch, cast) {
		return false
	}
	if last.ID == 0 || last.Role != ollama.RoleAssistant || strings.TrimSpace(last.Content) == "" {
		return false
	}
	if last.ID == ch.NudgedAt {
		return false
	}
	// A message with no time, from an import, has no age to be old by.
	if last.CreatedAt.IsZero() {
		return false
	}
	return now.Sub(last.CreatedAt) >= time.Duration(ch.WriteFirst)*time.Minute
}

// WriteFirstDueChats is every chat due to write first now, the one that has
// been quiet longest first: a store query for the chats set to, and
// WriteFirstDue for each.
func WriteFirstDueChats(st *store.Store, now time.Time) []store.Chat {
	if st == nil {
		return nil
	}
	candidates, err := st.ChatsWritingFirst()
	if err != nil {
		return nil
	}
	type due struct {
		chat store.Chat
		at   time.Time
	}
	var found []due
	for _, ch := range candidates {
		last, ok, err := st.LastMessage(ch.ID)
		if err != nil || !ok {
			continue
		}
		if WriteFirstDue(ch, castOf(st, ch), last, now) {
			found = append(found, due{ch, last.CreatedAt})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].at.Before(found[j].at) })
	out := make([]store.Chat, len(found))
	for i, d := range found {
		out[i] = d.chat
	}
	return out
}

// ErrMovedOn is returned by WriteFirst when the scene changed while the
// message was being written, so it was not stored: a message written for a
// silence that has since been broken would answer nothing.
var ErrMovedOn = errors.New("the scene moved on while it was being written")

// Wrote is what a character writing first left in a chat.
type Wrote struct {
	// Messages are the stored messages, oldest first: one, or in a group one
	// for each beat.
	Messages []store.Message
	// Who is whoever wrote, by name, and Text the start of it, for saying so.
	Who, Text string
}

// WriteFirst writes a scene's character speaking up after a silence, and
// stores it as theirs. It is not for the UI thread: it asks the model and waits.
//
// It builds the turn the way a reply is built, so it reads the same scene the
// same way, with a Nudge in a scene with one character and the cast carrying
// on in a group. It stores nothing when the model returns nothing, or when the
// chat's last message is no longer the one it started from.
func WriteFirst(ctx context.Context, client *ollama.Client, st *store.Store, cfg store.Config, ch store.Chat) (Wrote, error) {
	cast := castOf(st, ch)
	if !CanDraft(ch, cast) {
		return Wrote{}, fmt.Errorf("there is nobody in this scene to write first")
	}
	last, ok, err := st.LastMessage(ch.ID)
	if err != nil {
		return Wrote{}, err
	}
	if !ok {
		return Wrote{}, ErrMovedOn
	}
	cfg = playedAs(st, cfg, ch)
	model := ch.Model
	if model == "" {
		model = cfg.Model
	}
	if model == "" {
		return Wrote{}, fmt.Errorf("choose a model first")
	}

	group := len(cast) > 1
	stored, err := st.MessagesAfter(ch.ID, ch.SummaryUpto)
	if err != nil {
		return Wrote{}, err
	}
	var nameOf func(int64) string
	if group {
		byID := make(map[int64]string, len(cast))
		for _, c := range cast {
			byID[c.ID] = c.Name
		}
		nameOf = func(id int64) string { return byID[id] }
	}
	hist := History(stored, nameOf)
	msgs := BuildTurn(st, cfg, ch, cast, hist, Turn{Nudge: !group, Onward: group})

	// The same care as a reply: the model this scene uses, holding only it,
	// with room for the whole scene.
	client.UseForReplies(ctx, model)
	msgs = WithKnowledge(ctx, st, client, cfg, ch.Kind, msgs, hist)
	opts := FitContext(ctx, client, model, ch.Kind, OptionsFor(cfg, ch.Kind), msgs)
	NoteUsed(model, strconv.FormatInt(ch.ID, 10))

	think := cfg.Think
	chat := func(ctx context.Context, m []ollama.Message, d func(ollama.Delta)) (ollama.Message, ollama.Stats, error) {
		return client.Chat(ctx, model, m, opts, &think, d)
	}
	reply, stats, err := Stream(ctx, chat, ch.Kind, msgs, nil)
	// A loop stopped by the server keeps what came before it, as a reply does.
	if errors.Is(err, ollama.ErrRepeatLimit) && strings.TrimSpace(reply.Content) != "" {
		err = nil
	}
	if err != nil {
		return Wrote{}, err
	}
	content := strings.TrimSpace(reply.Content)
	thinking := reply.Thinking
	if inline, rest := ollama.SplitThinking(content); inline != "" {
		thinking, content = strings.TrimSpace(thinking+"\n\n"+inline), rest
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Wrote{}, fmt.Errorf("the model returned an empty reply")
	}

	// You may have written while it was being written, from here or from a
	// phone. Then this is a greeting for a conversation that has started.
	if now, ok, err := st.LastMessage(ch.ID); err != nil {
		return Wrote{}, err
	} else if !ok || now.ID != last.ID {
		return Wrote{}, ErrMovedOn
	}

	type piece struct {
		who     chars.Character
		text    string
		thought string
	}
	var pieces []piece
	if group {
		beats := chars.SplitBeats(content, chars.CastNames(cast))
		byName := make(map[string]chars.Character, len(cast))
		for _, member := range cast {
			byName[strings.ToLower(member.Name)] = member
		}
		for i, b := range beats {
			who, ok := byName[strings.ToLower(b.Name)]
			if !ok {
				who = cast[0]
			}
			p := piece{who: who, text: b.Text}
			if i == 0 {
				p.thought = thinking
			}
			pieces = append(pieces, p)
		}
		if len(pieces) == 0 {
			return Wrote{}, fmt.Errorf("the model returned an empty reply")
		}
	} else {
		pieces = []piece{{text: content, thought: thinking}}
	}

	var out Wrote
	var names []string
	for i, p := range pieces {
		m := store.Message{
			ChatID: ch.ID, Role: ollama.RoleAssistant, Content: p.text, Thinking: p.thought,
		}
		if group {
			m.CharacterID = p.who.ID
		}
		if i == len(pieces)-1 {
			m.EvalCount, m.TokPerSec = stats.Tokens, stats.TokPerSec
		}
		id, err := st.AddMessage(m)
		if err != nil {
			return out, err
		}
		m.ID, m.CreatedAt = id, time.Now()
		out.Messages = append(out.Messages, m)
		if group && !containsString(names, p.who.Name) {
			names = append(names, p.who.Name)
		}
	}
	if group {
		out.Who = strings.Join(names, ", ")
	} else {
		out.Who = cast[0].Name
	}
	out.Text = out.Messages[0].Content
	// The last one written, so that it is the one the next check finds last.
	return out, st.SetChatNudged(ch.ID, out.Messages[len(out.Messages)-1].ID)
}
