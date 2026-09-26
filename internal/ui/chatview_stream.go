package ui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
	"astral/internal/world"
)

// onSendClicked is both Send and Stop: the button changes meaning while a
// reply is streaming, because that is the only moment stopping is possible and
// a second permanent button would sit dead the rest of the time.
func (c *ChatView) onSendClicked() {
	if c.busy {
		c.Stop()
		return
	}
	c.Send()
}

// Send commits the composer's contents as a user turn and asks for a reply.
func (c *ChatView) Send() {
	text := strings.TrimSpace(c.composerText())
	// An image on its own is a complete message: "look at this" needs no words.
	if (text == "" && c.attachPath == "") || c.busy {
		return
	}
	if c.activeModel() == "" {
		c.fail("Choose a model first, click the model name under the message box.")
		return
	}
	if err := c.ensureChat(text); err != nil {
		c.fail("Could not start this chat: " + err.Error())
		return
	}

	// An attached image rides with this turn only. The base64 is not stored:
	// it would be megabytes per message in the database, and what actually
	// needs to survive is the model's description of the picture, which is in
	// the reply.
	if c.attachPath != "" {
		data, err := os.ReadFile(c.attachPath)
		if err != nil {
			c.fail("Could not read that image: " + err.Error())
		} else {
			c.pendingImage = base64.StdEncoding.EncodeToString(data)
			c.lastImage = c.attachPath
			note := "[attached an image: " + filepath.Base(c.attachPath) + "]"
			if text == "" {
				text = note + " Describe what you see, in detail."
			} else {
				text = text + "\n\n" + note
			}
		}
		c.AttachImage("")
	}

	row := c.appendRow(ollama.RoleUser, text, "", 0, time.Now())
	if id, err := c.store.AddMessage(store.Message{
		ChatID: c.chat.ID, Role: ollama.RoleUser, Content: text,
	}); err != nil {
		c.fail("Could not save your message: " + err.Error())
	} else {
		row.ID = id
	}
	c.setComposerText("")
	// Glide rather than snap: this is the one scroll the reader asked for by
	// pressing send, and travelling to it shows where their message went.
	c.glideToBottom()
	c.notifyChanged()
	// Housekeeping gives way: a recap the user cannot see is not worth making
	// them wait behind. The interrupted pass runs again after this reply.
	c.bg.yield()
	c.startStream()
}

// ensureChat creates the conversation row on the first message, titling it
// from what you actually wrote. Creating it lazily means opening the app and
// changing your mind does not leave an empty chat in the sidebar.
func (c *ChatView) ensureChat(firstMessage string) error {
	if c.chat.ID != 0 {
		return nil
	}
	// The world goes on the row as well: a scene in a world with no character
	// has nowhere else to record where it is, and without this it would reopen
	// tomorrow as a plain conversation with the setting gone.
	ch, err := c.store.NewChatIn(c.char.ID, c.chat.WorldID,
		store.TitleFrom(firstMessage), c.activeModel(), c.chat.Kind)
	if err != nil {
		return err
	}
	ch.CharacterName = c.char.Name
	ch.Accent = c.char.Accent
	if c.isGroup() {
		ids := make([]int64, 0, len(c.cast))
		for _, member := range c.cast {
			ids = append(ids, member.ID)
		}
		if err := c.store.SetCast(ch.ID, ids); err != nil {
			// The scene is playable without the row; what is lost is the other
			// characters when it is reopened, so it is worth saying so rather
			// than letting a group quietly become a two-hander tomorrow.
			c.fail("Could not save who is in this scene: " + err.Error())
		}
	}
	kind := c.chat.Kind
	c.chat = ch
	c.chat.Kind = kind

	// The greeting was shown as soon as the character was chosen, but there
	// was no chat to write it into until now. Persist it before your first
	// line so the transcript reloads in the order it was read.
	if c.greeting != nil && c.greeting.ID == 0 {
		if id, err := c.store.AddMessage(store.Message{
			ChatID: c.chat.ID, Role: ollama.RoleAssistant, Content: c.greeting.Text(),
			CharacterID: c.greeting.Speaker,
		}); err == nil {
			c.greeting.ID = id
		}
	}
	return nil
}

// ShowGreeting puts a character's opening message on screen for a chat that
// does not exist yet. It is not saved until the first reply is sent — opening
// a character to read their greeting and then changing your mind should not
// leave a chat behind.
func (c *ChatView) ShowGreeting(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	c.greetingAt = 0
	// Attributed in a group, so the opening is the first worked example of the
	// labelled format rather than an unnamed paragraph the model has to guess at.
	c.greeting = c.appendRowAs(c.greetingSpeaker(), ollama.RoleAssistant, text, "", 0, time.Now())
	// A character written with several ways into a scene should offer them.
	// Only while the greeting is the whole chat: once there is a reply under
	// it, changing the opening would rewrite the start of something already
	// being played.
	if len(chars.Greetings(c.char)) > 1 {
		c.greeting.AddAction(IconRegenerate, "Another opening", c.nextGreeting)
	}
	c.glideToBottom()
}

// nextGreeting steps to the character's next opening, wrapping at the end.
func (c *ChatView) nextGreeting() {
	if c.greeting == nil || c.busy {
		return
	}
	c.greetingAt++
	next := chars.GreetingAt(c.char, c.persona(), c.greetingAt)
	if next == "" {
		return
	}
	c.greeting.SetMarkdown(next)
	c.glideToBottom()
}

func (c *ChatView) persona() chars.Persona {
	return chars.Persona{
		Name:               c.cfg.PersonaName,
		Description:        c.cfg.PersonaDescription,
		GlobalInstructions: c.cfg.RulesText(),
		Style:              c.cfg.Style(),
	}
}

// history is the conversation as the model should see it.
//
// It comes from the database, not from the widget tree. Reading it off the
// rows made the on-screen transcript the source of truth for what gets sent,
// which is fragile in both directions: a row that is mid-stream or not yet
// saved had to be specially excluded, and anything the view chose not to
// render — a long scene is windowed — would silently vanish from the model's
// context. A query is about half a millisecond and cannot disagree with what
// was actually stored.
//
// Before the first send there is no chat row yet, so the greeting on screen is
// all there is; that case still reads from the view.
func (c *ChatView) history() []ollama.Message {
	if c.chat.ID == 0 {
		return c.rowHistory()
	}
	msgs, err := c.store.MessagesAfter(c.chat.ID, c.recapUpto)
	if err != nil {
		// Falling back keeps a scene playable through a transient database
		// error rather than sending the model an empty conversation.
		return c.rowHistory()
	}
	// Labelled and merged by internal/scene, which the phone uses too: the
	// speaker's name is stored beside a beat rather than inside it, and a
	// transcript that arrives without the labels teaches the model that replies
	// do not carry them.
	return scene.History(msgs, c.nameOf)
}

// rowHistory reads the transcript off the rows, skipping the one currently
// being streamed into.
func (c *ChatView) rowHistory() []ollama.Message {
	msgs := make([]store.Message, 0, len(c.rows))
	for _, r := range c.rows {
		if r == c.live || strings.TrimSpace(r.Text()) == "" {
			continue
		}
		msgs = append(msgs, store.Message{
			Role: r.Role, Content: r.Text(), CharacterID: r.Speaker,
		})
	}
	return scene.History(msgs, c.nameOf)
}

// buildRequest assembles the messages for a turn, framed for what this
// conversation is. A chat with no character is not a broken roleplay — it is
// either plain assistant talk or a design session, and each needs its own
// system message rather than an empty one.
func (c *ChatView) buildRequest() []ollama.Message {
	// Assembled by internal/scene, which the server the phone talks to uses
	// too. Two clients that build their own prompts answer the same scene
	// differently and throw away each other's cached prefix every time you
	// switch between them, so there is one assembler and this calls it.
	hist := c.history()
	// A continuation ends the prompt with the partial reply so the model
	// carries straight on from it, which means taking it out of the transcript
	// first: sent twice it reads as the character saying the same thing again.
	if c.continuing != nil && len(hist) > 0 && hist[len(hist)-1].Role == ollama.RoleAssistant {
		hist = hist[:len(hist)-1]
	}
	return scene.BuildFor(c.store, c.cfg, c.chat, c.cast, hist)
}

// budget divides this chat's context window between the parts of its prompt.
//
// The system prompt is measured rather than estimated: a rich character card
// and a bare one differ by thousands of characters, and the difference has to
// come out of the transcript rather than out of the window.
func (c *ChatView) budget(ca chars.Character, p chars.Persona) chars.Budget {
	numCtx := c.cfg.NumCtx
	if numCtx <= 0 {
		numCtx = chars.DefaultNumCtx
	}
	return chars.Plan(numCtx, c.cfg.NumPredict, len(chars.BuildSystem(ca, p)))
}

// recordStyle notes which style this scene is being written in.
func (c *ChatView) recordStyle() {
	if c.chat.ID == 0 {
		return
	}
	name := c.cfg.Style().Name
	if c.chat.StyleName == name {
		return
	}
	if err := c.store.SetChatStyle(c.chat.ID, name); err != nil {
		log.Printf("astral: recording the style for chat %d: %v", c.chat.ID, err)
		return
	}
	c.chat.StyleName = name
}

// options are the sampler settings, from internal/scene so that the window and
// the phone send the same ones.
func (c *ChatView) options() ollama.Options { return scene.Options(c.cfg) }

// startStream asks the model for a reply and streams it into a fresh row.
func (c *ChatView) startStream() {
	msgs := c.buildRequest()
	if len(msgs) == 0 {
		return
	}
	// Recorded after the request is built, so the "the style has changed"
	// notice reaches the model on the first reply after a change and is gone
	// by the second. Repeating it forever would be its own kind of drift.
	c.recordStyle()

	// The image goes on the most recent user turn, which is the one it was
	// attached to.
	if c.pendingImage != "" {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == ollama.RoleUser {
				msgs[i].Images = []string{c.pendingImage}
				break
			}
		}
		c.pendingImage = ""
	}

	// A scene whose own replies have stopped marking narration will keep not
	// marking it, however firmly the prompt asks: the transcript is the
	// strongest instruction in the context. Handing the model a reply that has
	// already begun inside an asterisk span settles it, because the next token
	// is narration whether or not the model meant to mark any.
	c.prefilled = false
	c.collapsed, c.collapseWhy = false, ""
	c.thinkStream = ollama.ThinkStream{}
	// A fresh splitter per turn, holding this scene's names.
	c.liveRows = nil
	c.beats = chars.BeatStream{}
	if c.isGroup() {
		c.beats.Names = c.castNames()
	}
	switch {
	case c.continuing != nil:
		// The reply so far is the prefill. Nothing else is needed: a model
		// handed an unfinished turn finishes it.
		msgs = append(msgs, ollama.Message{Role: ollama.RoleAssistant, Content: c.continuing.Text()})
	case !c.isGroup() && len(msgs) > 0 && msgs[len(msgs)-1].Role == ollama.RoleSystem && c.wantsPrefill(msgs):
		msgs = append(msgs, ollama.Message{Role: ollama.RoleAssistant, Content: chars.NarrationPrefill})
		c.prefilled = true
	}

	if c.continuing != nil {
		c.continuePrefix = c.continuing.Text()
		c.continueSpeaker = c.continuing.Speaker
		// The splitter starts mid-turn, so it is told whose turn it is.
		c.beats.Start(c.nameOf(c.continueSpeaker))
		c.live = c.continuing
		c.live.ContinueStreaming(c.streamWidth())
	} else {
		c.live = c.appendRow(ollama.RoleAssistant, "", "", 0, time.Now())
		c.live.BeginStreaming(c.streamWidth())
	}
	c.setBusy(true)

	model := c.activeModel()
	opts := c.options()
	// Sent explicitly either way. Left unspecified a reasoning model thinks by
	// default, which in roleplay means waiting through a paragraph of
	// deliberation for prose that reads no better — and under a reply limit it
	// can spend the whole budget thinking and return nothing at all.
	think := c.cfg.Think

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	gen := c.gen
	c.streamGen = gen
	started := time.Now()

	go func() {
		defer cancel()
		// onDelta runs on this goroutine, not the UI's. It must not touch a
		// widget — it only appends to the buffers the flush timer drains.
		msg, stats, err := c.client.Chat(ctx, model, msgs, opts, &think, func(d ollama.Delta) {
			c.pendMu.Lock()
			c.pendText.WriteString(d.Content)
			c.pendThink.WriteString(d.Thinking)
			c.pendMu.Unlock()
		})

		coreglib.IdleAdd(func() bool {
			c.finishStream(gen, msg, stats, err, started)
			return false
		})
	}()
}

// turnMeta is the footnote under a reply: its speed, and whether it was cut
// short. It takes stats by pointer because a reply that returned no token count
// has its elapsed time filled in here, and the caller stores what it is given.
func (c *ChatView) turnMeta(stats *ollama.Stats, started time.Time, cancelled bool) string {
	if stats.Tokens == 0 && !cancelled {
		stats.Elapsed = time.Since(started)
	}
	meta := ""
	if c.cfg.ShowStats {
		meta = stats.Summary()
	}
	if stats.Truncated() {
		// A reply that stops mid-sentence looks like the model failing unless
		// it says why.
		if meta != "" {
			meta += " · "
		}
		meta += "cut off at the reply limit"
	}
	if c.collapsed {
		// On the message as well as in a toast. The toast goes away and the
		// reply does not, and a month later this is the only thing that
		// explains why one turn in the transcript trails off into nonsense.
		if meta != "" {
			meta += " · "
		}
		meta += "stopped: the model began " + c.collapseWhy
	}
	return meta
}

// finishStream lands a completed reply on the main thread.
func (c *ChatView) finishStream(gen int, msg ollama.Message, stats ollama.Stats, err error, started time.Time) {
	// The staleness guard. Between the request going out and this running, the
	// view may have moved to a different chat — in which case this reply
	// belongs to a transcript that is no longer on screen, and appending it
	// here would put one scene's words into another's.
	if gen != c.gen {
		return
	}
	c.drainPending() // whatever arrived since the last tick
	c.setBusy(false)

	// Cleared here rather than where it is used, so that a turn which fails,
	// is cancelled, or returns nothing does not leave the next one thinking it
	// is still finishing something.
	continuing, prefix, spoke := c.continuing != nil, c.continuePrefix, c.continueSpeaker
	c.continuing, c.continuePrefix, c.continueSpeaker = nil, "", 0

	row := c.live
	c.live = nil
	if row == nil {
		return
	}
	row.EndStreaming()

	// errors.Is rather than a message match: the error arrives wrapped, so
	// comparing it directly misses, and comparing its text breaks whenever the
	// wording upstream changes. A deadline counts too, because to the person
	// waiting it is the same thing.
	cancelled := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	if err != nil && !cancelled {
		// A failed turn leaves nothing useful behind, so the empty row goes
		// with it rather than sitting in the transcript as a blank message.
		if strings.TrimSpace(row.Text()) == "" {
			c.removeRow(row)
		} else {
			row.SetMeta("stopped: " + err.Error())
		}
		c.fail(friendlyError(err))
		return
	}

	if c.collapsed {
		c.fail("The model started " + c.collapseWhy + ", so this reply was stopped. " +
			"Delete it and try again, or lower the temperature in Settings.")
	}

	// A stopped reply keeps what had already arrived: it is usually most of a
	// paragraph, and discarding it would throw away the model's work for the
	// sake of tidiness.
	// Some models write their deliberation into the reply rather than into the
	// field Ollama reserves for it. Move it where it belongs before anything
	// else looks at the text, so it is folded away rather than read as part of
	// the scene, and so the transcript stores the reply and not the model
	// talking to itself about its instructions.
	if tail, held := c.thinkStream.Done(); tail != "" || held != "" {
		if held != "" {
			row.AppendThinking(held)
		}
		if tail != "" {
			if c.isGroup() {
				c.streamBeats(tail)
			} else {
				row.AppendText(tail)
			}
		}
	}
	inlineThinking, content := ollama.SplitThinking(msg.Content)
	// A continuation is only the rest of a reply, so what gets rendered and
	// stored is what was already there plus what just arrived.
	if continuing {
		// Joined with nothing between them. The model was handed the reply as
		// it stood and carries on from exactly there, so it supplies its own
		// leading space when there should be one — and a reply cut mid-word
		// is finished mid-word.
		//
		// In a group the first half gets its label back before the two are
		// joined. Without it the finished reply opens with an unnamed beat, and
		// the split would hand the whole thing to whoever the cast lists first.
		if c.isGroup() {
			if name := c.nameOf(spoke); name != "" {
				content = chars.Label(name, prefix) + content
			} else {
				content = prefix + content
			}
		} else {
			content = prefix + content
		}
	}
	if inlineThinking != "" {
		msg.Thinking = strings.TrimSpace(msg.Thinking + "\n\n" + inlineThinking)
	}
	if c.isGroup() {
		c.finishGroupTurn(content, msg.Thinking, stats, started, cancelled)
		return
	}
	if text := strings.TrimSpace(content); text != "" {
		if c.prefilled {
			text = chars.RestorePrefill(text)
		}
		row.SetMarkdown(text)
	} else {
		row.SetMarkdown("")
	}
	if msg.Thinking != "" {
		row.SetThinking(msg.Thinking)
	}
	if strings.TrimSpace(row.Text()) == "" {
		c.removeRow(row)
		// A reasoning model can return a complete thought and no reply when
		// the token limit runs out mid-deliberation. Saying so beats leaving
		// an empty turn on screen with no explanation.
		if msg.Thinking != "" {
			c.fail("The model spent its whole reply limit thinking. Turn reasoning off, or raise the reply limit, in Settings.")
		} else if !cancelled {
			c.fail("The model returned an empty reply.")
		}
		return
	}

	row.SetMeta(c.turnMeta(&stats, started, cancelled))

	// A continued reply already has a row in the database, so it is rewritten
	// rather than added: saving it again would leave the scene holding the
	// first half twice.
	if row.ID != 0 {
		if err := c.store.SetMessageContent(row.ID, row.Text()); err != nil {
			c.fail("Could not save the reply: " + err.Error())
		}
	} else if id, err := c.store.AddMessage(store.Message{
		ChatID:    c.chat.ID,
		Role:      ollama.RoleAssistant,
		Content:   row.Text(),
		Thinking:  row.Thinking(),
		EvalCount: stats.Tokens,
		TokPerSec: stats.TokPerSec,
	}); err != nil {
		c.fail("Could not save the reply: " + err.Error())
	} else {
		row.ID = id
	}
	c.notifyChanged()
	if c.atBottom() {
		c.scrollToBottom()
	}
	c.checkModelFits(c.activeModel())
	// Compaction first, and only one of the two can run: a scene that has
	// outgrown its window needs the recap before it needs new lore, because
	// without it the next turn starts dropping the oldest messages unread.
	c.maybeCompact()
	c.maybeLearn()
}

// maybeLearn teaches the world's lorebook from the scene.
//
// Like compaction it runs after a reply rather than before the next one, so it
// costs reading time rather than waiting time, and it only runs every few
// turns because most turns establish nothing that outlives them.
func (c *ChatView) maybeLearn() {
	if c.bg.running || c.chat.ID == 0 || c.world.ID == 0 || c.char.Name == "" {
		return
	}
	chatID := c.chat.ID
	fresh, err := c.store.MessagesAfter(chatID, c.chat.LoreUpto)
	if err != nil || len(fresh) < world.LearnEveryTurns*2 {
		return
	}
	upto := fresh[len(fresh)-1].ID
	turns := make([]ollama.Message, 0, len(fresh))
	for _, m := range fresh {
		turns = append(turns, ollama.Message{Role: m.Role, Content: m.Content})
	}

	ctx, ok := c.bg.take("lorebook", learnTimeout)
	if !ok {
		return
	}
	client, model := c.client, c.housekeepingModel()
	w, existing := c.world, c.lore
	charName := c.char.Name
	userName := c.cfg.PersonaName
	if userName == "" {
		userName = chars.DefaultPersonaName
	}
	opts := c.options()

	go func() {
		learned, err := world.Learn(ctx, client, model, w, existing, turns, charName, userName, opts)

		coreglib.IdleAdd(func() bool {
			c.bg.done()
			if err != nil {
				if ctx.Err() != nil {
					return false // the user's turn took the lane; try again later
				}
				// Quiet: the scene is unaffected, and the next pass tries
				// again. Interrupting a reply to report that background
				// note-taking failed would be the wrong trade.
				log.Printf("astral: learning lore for world %d: %v", w.ID, err)
				return false
			}
			// Marked as taught either way. A pass that found nothing is a
			// normal outcome, and re-examining the same turns would find
			// nothing again at the same cost.
			if err := c.store.SetChatLoreUpto(chatID, upto); err != nil {
				log.Printf("astral: recording lore progress: %v", err)
			}
			if c.chat.ID == chatID {
				c.chat.LoreUpto = upto
			}

			kept, held := 0, 0
			for _, e := range learned {
				if _, err := c.store.SaveLoreEntry(e); err != nil {
					// A hand-written entry refusing an automatic update is
					// the intended behaviour, not a failure.
					if err != store.ErrWouldOverwriteManual {
						log.Printf("astral: saving lore %q: %v", e.Name, err)
					}
					continue
				}
				if e.Enabled {
					kept++
				} else {
					held++
				}
			}
			c.checkModelFits(model)
			if kept+held > 0 {
				log.Printf("astral: learned %d lore entries (%d applied, %d held for review)",
					kept+held, kept, held)
				if entries, err := c.store.LoreEntries(w.ID); err == nil {
					c.lore = entries
				}
				if c.OnLoreLearned != nil {
					c.OnLoreLearned(kept, held)
				}
			}
			return false
		})
	}()
}

// maybeCompact folds the older half of an overlong scene into the recap.
//
// It runs after a reply has landed rather than before the next one is sent,
// so the cost is paid while you are reading rather than while you are waiting.
// If you send again before it finishes, that turn simply goes out with the
// transcript as it stands.
func (c *ChatView) maybeCompact() {
	if c.bg.running || c.chat.ID == 0 || !c.compactable() {
		return
	}
	chatID := c.chat.ID
	stored, err := c.store.MessagesAfter(chatID, c.recapUpto)
	if err != nil || len(stored) == 0 {
		return
	}
	// Labelled, so the recap can say which of them did what. A group's turns
	// summarised without their names come back as things "the group" did, and
	// which of five people admitted something is the detail a scene turns on.
	wire := scene.History(stored, c.nameOf)
	budget := c.sceneBudget()
	aged, _ := chars.SplitForCompaction(wire, budget)
	if len(aged) == 0 {
		return
	}
	// The recap will cover everything up to and including this message.
	upto := stored[len(aged)-1].ID

	ctx, ok := c.bg.take("recap", compactTimeout)
	if !ok {
		return
	}
	client, model := c.client, c.housekeepingModel()
	prev, cast, persona, opts := c.recap, c.sceneCast(), c.persona(), c.options()
	plain := c.chat.Kind == store.KindAssistant

	go func() {
		// A conversation and a scene need different questions asked of the
		// summariser. Keeping "the state of the relationship" out of the record
		// of an hour spent working through a problem is the whole difference.
		compact := chars.CompactFor
		if plain {
			compact = func(ctx context.Context, cl *ollama.Client, model, previous string,
				aged []ollama.Message, _ []chars.Character, p chars.Persona,
				opts ollama.Options, budget chars.Budget) (string, error) {
				return chars.CompactPlain(ctx, cl, model, previous, aged, p, opts, budget)
			}
		}
		next, err := compact(ctx, client, model, prev, aged, cast, persona, opts, budget)

		coreglib.IdleAdd(func() bool {
			c.bg.done()
			if err != nil {
				if ctx.Err() != nil {
					return false // the user's turn took the lane; try again later
				}
				// Not surfaced: the scene still works without it, and the next
				// turn will try again. Failing loudly here would interrupt
				// reading a reply to report a background housekeeping problem.
				log.Printf("astral: compacting %d: %v", chatID, err)
				return false
			}
			// Saved against the chat it was built from, even if the view has
			// moved on since — the work is still correct for that scene.
			if err := c.store.SetChatSummary(chatID, next, upto); err != nil {
				log.Printf("astral: saving recap for %d: %v", chatID, err)
				return false
			}
			if c.chat.ID == chatID {
				c.recap, c.recapUpto = next, upto
			}
			log.Printf("astral: compacted %d turns of chat %d into a %d-character recap using %s",
				len(aged), chatID, len(next), model)
			c.checkModelFits(model)
			return false
		})
	}()
}

// friendlyError turns the client's error into something worth reading. The
// underlying messages are accurate but assume you know what a transport or a
// 404 from /api/chat means.
func friendlyError(err error) string {
	s := err.Error()
	switch {
	case errors.Is(err, ollama.ErrUnreachable):
		return "Ollama isn't running. Start it with `ollama serve`, then try again."
	case strings.Contains(s, "not found") || strings.Contains(s, "404"):
		return "That model isn't installed. Pull it with `ollama pull <model>` and try again."
	case strings.Contains(s, "memory") || strings.Contains(s, "requires more"):
		return "The model needs more memory than is free. Try a smaller model or a lower context size."
	default:
		return s
	}
}

// Stop cancels an in-flight reply, keeping whatever has already arrived.
func (c *ChatView) Stop() {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

// setBusy toggles the streaming affordances and starts or stops the flush
// timer that drains streamed tokens into the label.
func (c *ChatView) setBusy(busy bool) {
	if c.busy == busy {
		return
	}
	c.busy = busy
	if busy {
		c.sendBtn.SetIconName(IconStop)
		c.sendBtn.SetTooltipText("Stop generating")
		c.sendBtn.AddCSSClass("stopping")
		c.sendBtn.SetSensitive(true)
		c.startFlush()
	} else {
		c.stopFlush()
		c.sendBtn.SetIconName(IconSend)
		c.sendBtn.SetTooltipText("Send (Enter)")
		c.sendBtn.RemoveCSSClass("stopping")
		c.sendBtn.SetSensitive(strings.TrimSpace(c.composerText()) != "")
		c.focusComposer()
	}
}

func (c *ChatView) startFlush() {
	if c.flushID != 0 {
		return
	}
	c.flushID = coreglib.TimeoutAdd(flushInterval, func() bool {
		if !c.busy {
			c.flushID = 0
			return false
		}
		c.drainPending()
		return true
	})
}

func (c *ChatView) stopFlush() {
	if c.flushID == 0 {
		return
	}
	coreglib.SourceRemove(c.flushID)
	c.flushID = 0
}

// drainPending moves buffered tokens into the live row. Runs on the main
// thread only — from the flush timer, and once more at completion.
func (c *ChatView) drainPending() {
	c.pendMu.Lock()
	text := c.pendText.String()
	think := c.pendThink.String()
	c.pendText.Reset()
	c.pendThink.Reset()
	c.pendMu.Unlock()

	if c.live == nil || (text == "" && think == "") {
		return
	}
	// A model whose deliberation arrives in the reply rather than in its own
	// field is folded away as it streams, not at the end. What it thinks about
	// first is its own instructions, so what would otherwise stream past is the
	// character sheet and the formatting rules read back aloud.
	if text != "" {
		var inline string
		text, inline = c.thinkStream.Next(text)
		think += inline
	}
	stick := c.atBottom() // decided before the append changes the extent
	if think != "" {
		// On the row the turn started in. Deliberation belongs to the reply
		// rather than to whichever character happens to be speaking when it
		// arrives.
		if len(c.liveRows) > 0 {
			c.liveRows[0].row.AppendThinking(think)
		} else {
			c.live.AppendThinking(think)
		}
	}
	if text != "" {
		// Plain text while streaming: a half-arrived "**bo" is not valid
		// markup, and rendering per flush would flicker between broken and
		// correct formatting. It is rendered once, at the end.
		if c.isGroup() {
			// A group reply arrives as one stream with the speakers marked in
			// it, and is dealt out into a row each as those marks appear.
			c.streamBeats(text)
		} else {
			c.live.AppendText(text)
			c.live.SetMeta("")
		}
	}
	// A model that has come apart will not recover on its own, and every
	// further token is both wasted and destined for the transcript that becomes
	// the next turn's prompt. Stopping here costs one wasted reply instead of
	// poisoning the scene.
	//
	// Both channels are watched. A model can spend its whole budget rambling in
	// its reasoning and never reach the reply at all, and until now nothing was
	// looking at that text.
	if !c.collapsed {
		if why := brokenWhy(c.live.Text()); why != "" {
			c.collapsed, c.collapseWhy = true, why
		} else if why := brokenWhy(c.live.Thinking()); why != "" {
			c.collapsed, c.collapseWhy = true, why
		}
		if c.collapsed {
			c.Stop()
		}
	}
	if stick {
		c.scrollToBottom()
	}
}

// brokenWhy names the way a stream has stopped being a reply, in words fit to
// show someone, or returns empty if it has not.
func brokenWhy(s string) string {
	switch {
	case Looping(s):
		return "repeating itself"
	case Rambling(s):
		return "running on without finishing a sentence"
	}
	return ""
}

// regenerate rewrites a reply: the turn and everything after it are dropped,
// then the model is asked again from that point.
func (c *ChatView) regenerate(row *MessageRow) {
	if c.busy {
		return
	}
	idx := c.indexOf(row)
	if idx < 0 {
		return
	}
	if row.ID != 0 {
		if err := c.store.DeleteMessagesFrom(c.chat.ID, row.ID); err != nil {
			c.fail("Could not rewind the chat: " + err.Error())
			return
		}
	}
	for _, r := range c.rows[idx:] {
		c.column.Remove(r.Widget())
	}
	c.rows = c.rows[:idx]
	c.startStream()
}

// continueReply asks for the rest of a reply that stopped at the token limit.
//
// Only the last one. Continuing a turn from the middle of a scene would mean
// everything after it was answering a shorter version of it, and the rest of
// the transcript would quietly stop following.
func (c *ChatView) continueReply(row *MessageRow) {
	if c.busy || row == nil || row.Role != ollama.RoleAssistant {
		return
	}
	if len(c.rows) == 0 || c.rows[len(c.rows)-1] != row {
		c.fail("Only the last reply can be continued.")
		return
	}
	if strings.TrimSpace(row.Text()) == "" {
		return
	}
	c.continuing = row
	c.startStream()
}

// deleteRow removes a single turn from the transcript and the database.
func (c *ChatView) deleteRow(row *MessageRow) {
	if c.busy {
		return
	}
	if row.ID != 0 {
		if err := c.store.DeleteMessage(row.ID); err != nil {
			c.fail("Could not delete that message: " + err.Error())
			return
		}
	}
	c.removeRow(row)
	c.notifyChanged()
}

func (c *ChatView) removeRow(row *MessageRow) {
	idx := c.indexOf(row)
	if idx < 0 {
		return
	}
	c.column.Remove(row.Widget())
	c.rows = append(c.rows[:idx], c.rows[idx+1:]...)
}

func (c *ChatView) indexOf(row *MessageRow) int {
	for i, r := range c.rows {
		if r == row {
			return i
		}
	}
	return -1
}

func (c *ChatView) notifyChanged() {
	if c.OnChatChanged != nil {
		c.OnChatChanged()
	}
}

func (c *ChatView) fail(msg string) {
	if c.OnError != nil {
		c.OnError(msg)
	}
}

// wantsPrefill reports whether this turn should hand the model a reply that has
// already started inside a narration span.
//
// Only a roleplay scene, only a character, and only when the recent replies
// have actually drifted. A prefill costs the model a little freedom over how
// to open a reply, which is not worth spending on a scene that is behaving.
func (c *ChatView) wantsPrefill(msgs []ollama.Message) bool {
	if c.char.Name == "" || c.chat.Kind == store.KindDesigner ||
		c.chat.Kind == store.KindAssistant || c.chat.Kind == store.KindStyleDesigner {
		return false
	}
	return chars.NarrationDrifted(c.history())
}

// housekeepingModel is the model that writes the recap and reads the scene for
// lore. It falls back to whichever model is playing the scene, which is what
// an empty setting means and also what happens when the configured one has
// been deleted since it was chosen.
func (c *ChatView) housekeepingModel() string {
	m := strings.TrimSpace(c.cfg.HousekeepingModel)
	if m == "" {
		return c.activeModel()
	}
	return m
}

// checkModelFits says so, once, when a model did not fit in video memory and
// is running partly on the CPU.
//
// The one hardware problem that hides: a model that half fits does not fail,
// it gets several times slower, and nothing connects that to a context size
// raised a week ago. Measured on a 24GB card, a 27B at 32k left no room for a
// 4B beside it and the 4B fell to 18% CPU.
//
// Context size alone costs memory, not time: 4k to 64k measured flat within
// noise while video memory moved four gigabytes.
func (c *ChatView) checkModelFits(model string) {
	if c.warnedSpill || model == "" {
		return
	}
	scene := model == c.activeModel()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		loaded, err := c.client.Running(ctx)
		if err != nil {
			return
		}
		l, ok := ollama.FindLoaded(loaded, model)
		if !ok || !l.Spilled() {
			return
		}
		coreglib.IdleAdd(func() bool {
			if c.warnedSpill {
				return false
			}
			c.warnedSpill = true
			if scene {
				c.fail(fmt.Sprintf(
					"%s only fits %.0f%% in video memory, so it is running partly on the CPU and "+
						"is much slower than it could be. Lower the context size in Settings, "+
						"or use a smaller model.",
					shortModel(model), 100*l.OnGPU()))
			} else {
				c.fail(fmt.Sprintf(
					"%s only fits %.0f%% in video memory beside %s, so the background work is "+
						"running partly on the CPU. Use a smaller background model, or lower "+
						"the context size.",
					shortModel(model), 100*l.OnGPU(), shortModel(c.activeModel())))
			}
			return false
		})
	}()
}
