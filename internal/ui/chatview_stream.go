package ui

import (
	"context"
	"encoding/base64"
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
	kind := c.chat.Kind
	c.chat = ch
	c.chat.Kind = kind

	// The greeting was shown as soon as the character was chosen, but there
	// was no chat to write it into until now. Persist it before your first
	// line so the transcript reloads in the order it was read.
	if c.greeting != nil && c.greeting.ID == 0 {
		if id, err := c.store.AddMessage(store.Message{
			ChatID: c.chat.ID, Role: ollama.RoleAssistant, Content: c.greeting.Text(),
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
	c.greeting = c.appendRow(ollama.RoleAssistant, text, "", 0, time.Now())
	c.glideToBottom()
}

func (c *ChatView) persona() chars.Persona {
	return chars.Persona{
		Name:               c.cfg.PersonaName,
		Description:        c.cfg.PersonaDescription,
		GlobalInstructions: c.cfg.GlobalInstructions,
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
	out := make([]ollama.Message, 0, len(msgs))
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		out = append(out, ollama.Message{Role: m.Role, Content: m.Content})
	}
	return out
}

// rowHistory reads the transcript off the rows, skipping the one currently
// being streamed into.
func (c *ChatView) rowHistory() []ollama.Message {
	out := make([]ollama.Message, 0, len(c.rows))
	for _, r := range c.rows {
		if r == c.live || strings.TrimSpace(r.Text()) == "" {
			continue
		}
		out = append(out, ollama.Message{Role: r.Role, Content: r.Text()})
	}
	return out
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
	return scene.Build(c.store, c.cfg, c.chat, c.char, c.history())
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
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == ollama.RoleSystem && c.wantsPrefill(msgs) {
		msgs = append(msgs, ollama.Message{Role: ollama.RoleAssistant, Content: chars.NarrationPrefill})
		c.prefilled = true
	}

	c.live = c.appendRow(ollama.RoleAssistant, "", "", 0, time.Now())
	c.live.BeginStreaming(c.streamWidth())
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

	row := c.live
	c.live = nil
	if row == nil {
		return
	}
	row.EndStreaming()

	cancelled := err != nil && (strings.Contains(err.Error(), "context canceled") || err == context.Canceled)
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
	inlineThinking, content := SplitThinking(msg.Content)
	if inlineThinking != "" {
		msg.Thinking = strings.TrimSpace(msg.Thinking + "\n\n" + inlineThinking)
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
	row.SetMeta(meta)

	if id, err := c.store.AddMessage(store.Message{
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
	if c.bg.running || c.chat.ID == 0 || c.char.Name == "" {
		return
	}
	chatID := c.chat.ID
	stored, err := c.store.MessagesAfter(chatID, c.recapUpto)
	if err != nil || len(stored) == 0 {
		return
	}
	wire := make([]ollama.Message, 0, len(stored))
	for _, m := range stored {
		wire = append(wire, ollama.Message{Role: m.Role, Content: m.Content})
	}
	budget := c.budget(c.char, c.persona())
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
	prev, char, persona, opts := c.recap, c.char, c.persona(), c.options()

	go func() {
		next, err := chars.Compact(ctx, client, model, prev, aged, char, persona, opts, budget)

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
	case strings.Contains(s, "cannot reach Ollama"):
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
	stick := c.atBottom() // decided before the append changes the extent
	if think != "" {
		c.live.AppendThinking(think)
	}
	if text != "" {
		// Plain text while streaming: a half-arrived "**bo" is not valid
		// markup, and rendering per flush would flicker between broken and
		// correct formatting. It is rendered once, at the end.
		c.live.AppendText(text)
		c.live.SetMeta("")

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
// This is the one hardware problem that hides. A model that half fits does not
// fail, it just gets several times slower, and nothing on screen connects that
// to the context size someone raised a week ago. Measured on a 24GB card: a
// 27B at a 32k window left no room for a 4B beside it, and the 4B dropped to
// 18% CPU, where it was slower than the model it was meant to be faster than.
//
// It is also the check behind having a separate housekeeping model at all.
// That is only worth doing while Ollama can hold both at once — it keeps them
// resident rather than swapping — and the saving is real right up to the point
// where one of them spills.
//
// What does not change speed, incidentally, is the context size on its own.
// Measured across 4k to 64k with the same prompt, prompt and generation rates
// were flat within noise while video memory moved by four gigabytes. A window
// costs memory, not time; what costs time is the prompt actually sent.
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
