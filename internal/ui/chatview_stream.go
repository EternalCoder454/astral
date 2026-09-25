package ui

import (
	"context"
	"encoding/base64"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"astral/internal/chars"
	"astral/internal/ollama"
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
	c.scrollToBottom()
	c.notifyChanged()
	c.startStream()
}

// ensureChat creates the conversation row on the first message, titling it
// from what you actually wrote. Creating it lazily means opening the app and
// changing your mind does not leave an empty chat in the sidebar.
func (c *ChatView) ensureChat(firstMessage string) error {
	if c.chat.ID != 0 {
		return nil
	}
	ch, err := c.store.NewChat(c.char.ID, store.TitleFrom(firstMessage), c.activeModel(), c.chat.Kind)
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
	c.scrollToBottom()
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
	hist := c.history()
	system := func(content string) []ollama.Message {
		return append([]ollama.Message{{Role: ollama.RoleSystem, Content: content}}, hist...)
	}
	switch c.chat.Kind {
	case store.KindDesigner:
		return system(chars.DesignerSystem)
	case store.KindStyleDesigner:
		return system(chars.StyleDesignerSystem)
	case store.KindAssistant:
		return system(chars.AssistantSystem)
	}
	if c.char.Name == "" {
		return system(chars.AssistantSystem)
	}
	return chars.BuildMessages(c.char, chars.Scene{
		Persona: c.persona(),
		Lore:    c.loreFor(hist),
		Recap:   c.recap,
		History: hist,
	})
}

func (c *ChatView) options() ollama.Options {
	return ollama.Options{
		Temperature:   c.cfg.Temperature,
		TopP:          c.cfg.TopP,
		TopK:          c.cfg.TopK,
		RepeatPenalty: c.cfg.RepeatPenalty,
		NumCtx:        c.cfg.NumCtx,
		NumPredict:    c.cfg.NumPredict,
	}
}

// startStream asks the model for a reply and streams it into a fresh row.
func (c *ChatView) startStream() {
	msgs := c.buildRequest()
	if len(msgs) == 0 {
		return
	}

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

	// A stopped reply keeps what had already arrived: it is usually most of a
	// paragraph, and discarding it would throw away the model's work for the
	// sake of tidiness.
	if text := strings.TrimSpace(msg.Content); text != "" {
		row.SetMarkdown(text)
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
	c.maybeCompact()
	c.maybeLearn()
}

// maybeLearn teaches the world's lorebook from the scene.
//
// Like compaction it runs after a reply rather than before the next one, so it
// costs reading time rather than waiting time, and it only runs every few
// turns because most turns establish nothing that outlives them.
func (c *ChatView) maybeLearn() {
	if c.learning || c.chat.ID == 0 || c.world.ID == 0 || c.char.Name == "" {
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

	c.learning = true
	client, model := c.client, c.activeModel()
	w, existing := c.world, c.lore
	charName := c.char.Name
	userName := c.cfg.PersonaName
	if userName == "" {
		userName = chars.DefaultPersonaName
	}
	opts := c.options()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), learnTimeout)
		defer cancel()
		learned, err := world.Learn(ctx, client, model, w, existing, turns, charName, userName, opts)

		coreglib.IdleAdd(func() bool {
			c.learning = false
			if err != nil {
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
	if c.compacting || c.chat.ID == 0 || c.char.Name == "" {
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
	aged, _ := chars.SplitForCompaction(wire)
	if len(aged) == 0 {
		return
	}
	// The recap will cover everything up to and including this message.
	upto := stored[len(aged)-1].ID

	c.compacting = true
	client, model := c.client, c.activeModel()
	prev, char, persona, opts := c.recap, c.char, c.persona(), c.options()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), compactTimeout)
		defer cancel()
		next, err := chars.Compact(ctx, client, model, prev, aged, char, persona, opts)

		coreglib.IdleAdd(func() bool {
			c.compacting = false
			if err != nil {
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
			log.Printf("astral: compacted %d turns of chat %d into a %d-character recap",
				len(aged), chatID, len(next))
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
	if stick {
		c.scrollToBottom()
	}
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
