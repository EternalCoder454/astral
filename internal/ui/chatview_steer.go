package ui

import (
	"context"
	"strings"
	"sync"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Steering a scene one turn at a time, the controls every other roleplay app
// has: Write for Me, Rewrite with a Note, Pin, Branch from Here, and in a group,
// choosing who answers or letting the cast carry on without you.

// withTurn runs start with a turn's steering in place. start builds its request
// before it returns, whether or not a reply follows, so the steering applies
// to exactly that request and never leaks into the next one.
func (c *ChatView) withTurn(t scene.Turn, start func()) {
	c.turn = t
	start()
	c.turn = scene.Turn{}
}

// rewriteWithNote writes a reply again toward something you asked for.
func (c *ChatView) rewriteWithNote(row *MessageRow) {
	if c.busy {
		return
	}
	AskRewriteNote(c.widget, func(note string) {
		c.withTurn(scene.Turn{Note: note}, func() { c.regenerate(row) })
	})
}

// togglePin pins a message, or unpins one that is.
func (c *ChatView) togglePin(row *MessageRow) {
	if row.ID == 0 {
		c.fail("Wait for the reply to finish before pinning it.")
		return
	}
	pin := !row.Pinned
	if err := c.store.SetMessagePinned(row.ID, pin); err != nil {
		c.fail("Could not pin that message: " + err.Error())
		return
	}
	row.SetPinned(pin)
	if pin {
		c.notice("Pinned. This scene will keep it in mind however long it grows.")
	} else {
		c.notice("Unpinned.")
	}
}

// branchFrom starts a new chat that is this one up to a message.
func (c *ChatView) branchFrom(row *MessageRow) {
	if c.busy {
		c.fail("Wait for the reply to finish before branching.")
		return
	}
	if row.ID == 0 || c.chat.ID == 0 {
		c.fail("Send a message first: there is nothing to branch from yet.")
		return
	}
	if c.OnBranch != nil {
		c.OnBranch(c.chat.ID, row.ID)
	}
}

// Speak sends what is in the message box with somebody chosen to answer, or,
// with the box empty, has them carry the scene on without you. An empty name
// lets the scene choose.
func (c *ChatView) Speak(name string) {
	if c.busy || c.drafting {
		return
	}
	if strings.TrimSpace(c.composerText()) != "" || c.attachPath != "" || len(c.files) > 0 {
		c.withTurn(scene.Turn{Speaker: name}, c.Send)
		return
	}
	if c.activeModel() == "" {
		c.fail("Choose a model first, click the model name under the message box.")
		return
	}
	if err := c.ensureChat(""); err != nil {
		c.fail("Could not start this chat: " + err.Error())
		return
	}
	c.bg.yield()
	c.withTurn(scene.Turn{Speaker: name, Onward: true}, c.startStream)
}

// draftFlush is how often, in milliseconds, a draft being written is put in
// the message box.
const draftFlush = 50

// canDraft reports whether Write for Me belongs in this chat.
func (c *ChatView) canDraft() bool {
	return scene.CanDraft(c.chat, c.sceneCast())
}

// refreshDraftButton shows Write for Me in a scene, and shows it as Stop while
// a draft is being written.
func (c *ChatView) refreshDraftButton() {
	if c.draftBtn == nil {
		return
	}
	c.draftBtn.SetVisible(c.canDraft())
	switch {
	case c.drafting:
		c.draftBtn.SetIconName(IconStop)
		c.draftBtn.SetTooltipText("Stop writing")
	case strings.TrimSpace(c.composerText()) != "":
		// With something typed, the same button makes it better rather than
		// replacing it: a finished message comes back improved, and a note of
		// what you meant comes back written out.
		c.draftBtn.SetIconName(IconRegenerate)
		c.draftBtn.SetTooltipText("Rewrite what you typed, better")
	default:
		c.draftBtn.SetIconName(IconDraft)
		c.draftBtn.SetTooltipText("Write for Me: draft your next message")
	}
	c.draftBtn.SetSensitive(!c.busy)
}

// WriteForMe drafts your next message into the message box, from what is
// already typed there when anything is.
//
// The draft is only ever put in the box, never sent: it is a suggestion in
// your voice, and whether it says what you meant is yours to decide.
func (c *ChatView) WriteForMe() {
	if c.drafting {
		if c.draftCancel != nil {
			c.draftCancel()
		}
		return
	}
	if c.busy || !c.canDraft() {
		return
	}
	model := c.activeModel()
	if model == "" {
		c.fail("Choose a model first, click the model name under the message box.")
		return
	}
	idea := strings.TrimSpace(c.composerText())
	msgs := scene.Draft(c.store, c.cfg, c.chat, c.sceneCast(), c.history(), idea)
	opts := scene.DraftOptions(c.cfg, c.chat.Kind)
	userName := c.youName()

	ctx, cancel := context.WithCancel(context.Background())
	c.draftCancel = cancel
	c.drafting = true
	c.refreshDraftButton()
	c.sendBtn.SetSensitive(false)
	c.setComposerText("")
	c.placeholder.SetText("Writing your message…")
	c.placeholder.SetVisible(true)

	var mu sync.Mutex
	var sofar strings.Builder
	var think ollama.ThinkStream
	done := false
	// The box is refreshed on a timer rather than per token, as a reply is.
	coreglib.TimeoutAdd(draftFlush, func() bool {
		mu.Lock()
		text, finished := sofar.String(), done
		mu.Unlock()
		if text != "" && text != c.composerText() {
			c.setComposerText(chars.CleanDraft(text, userName))
		}
		return !finished
	})

	client := c.client
	go func() {
		defer cancel()
		client.UseForReplies(ctx, model)
		noThink := false
		msg, _, err := client.Chat(ctx, model, msgs, opts, &noThink, func(d ollama.Delta) {
			shown, _ := think.Next(d.Content)
			mu.Lock()
			if sofar.Len() < scene.DraftChars {
				sofar.WriteString(shown)
			}
			mu.Unlock()
		})
		mu.Lock()
		done = true
		text := sofar.String()
		mu.Unlock()
		coreglib.IdleAdd(func() {
			c.drafting = false
			c.draftCancel = nil
			c.refreshPlaceholder()
			c.refreshDraftButton()
			final := chars.CleanDraft(text, userName)
			if strings.TrimSpace(final) == "" {
				final = chars.CleanDraft(msg.Content, userName)
			}
			switch {
			case strings.TrimSpace(final) != "":
				c.setComposerText(final)
			case err != nil && ctx.Err() == nil:
				c.setComposerText(idea)
				c.fail("Could not write a draft: " + friendlyError(err))
			default:
				// Stopped before a word: what you had typed comes back.
				c.setComposerText(idea)
			}
			c.sendBtn.SetSensitive(strings.TrimSpace(c.composerText()) != "")
			c.focusComposer()
			// The cursor at the end, where you would carry on typing.
			buf := c.composer.Buffer()
			buf.PlaceCursor(buf.EndIter())
		})
	}()
}

// rewriteMine rewrites a message you sent, better, in place. Your last
// message is then answered again, since the reply was to the old one; one
// further up is only reworded, because everything after it already happened.
func (c *ChatView) rewriteMine(row *MessageRow) {
	if c.busy || c.drafting {
		return
	}
	if row.ID == 0 || c.chat.ID == 0 {
		c.fail("Send it first: there is nothing stored to rewrite yet.")
		return
	}
	model := c.activeModel()
	if model == "" {
		c.fail("Choose a model first, click the model name under the message box.")
		return
	}
	hist, err := scene.HistoryBefore(c.store, c.chat, c.nameOf, row.ID)
	if err != nil {
		c.fail("Could not read the chat: " + err.Error())
		return
	}
	original := row.Text()
	msgs := scene.Draft(c.store, c.cfg, c.chat, c.sceneCast(), hist, original)
	opts := scene.DraftOptions(c.cfg, c.chat.Kind)
	userName := c.youName()

	ctx, cancel := context.WithCancel(context.Background())
	c.draftCancel = cancel
	c.drafting = true
	c.refreshDraftButton()
	c.sendBtn.SetSensitive(false)
	c.notice("Rewriting your message…")

	client := c.client
	go func() {
		defer cancel()
		client.UseForReplies(ctx, model)
		noThink := false
		var think ollama.ThinkStream
		var sofar strings.Builder
		_, _, err := client.Chat(ctx, model, msgs, opts, &noThink, func(d ollama.Delta) {
			shown, _ := think.Next(d.Content)
			if sofar.Len() < scene.DraftChars*2 {
				sofar.WriteString(shown)
			}
		})
		text := chars.CleanDraft(sofar.String(), userName)
		coreglib.IdleAdd(func() {
			c.drafting = false
			c.draftCancel = nil
			c.refreshDraftButton()
			c.sendBtn.SetSensitive(strings.TrimSpace(c.composerText()) != "")
			if strings.TrimSpace(text) == "" {
				if err != nil && ctx.Err() == nil {
					c.fail("Could not rewrite it: " + friendlyError(err))
				}
				return
			}
			if err := c.store.SetMessageContent(row.ID, text); err != nil {
				c.fail("Could not save the rewrite: " + err.Error())
				return
			}
			row.SetMarkdown(text)
			c.notifyChanged()
			// Your last message, answered: the reply after it is written
			// again, to the message as it now reads.
			idx := c.indexOf(row)
			if idx >= 0 && idx+1 < len(c.rows) && c.lastUserRow() == row {
				c.regenerate(c.rows[idx+1])
				return
			}
			c.notice("Rewritten.")
		})
	}()
}

// lastUserRow is your newest message on screen, or nil.
func (c *ChatView) lastUserRow() *MessageRow {
	for i := len(c.rows) - 1; i >= 0; i-- {
		if c.rows[i].Role == ollama.RoleUser {
			return c.rows[i]
		}
	}
	return nil
}

// notice says an action worked, in a toast.
func (c *ChatView) notice(msg string) {
	if c.OnNotice != nil {
		c.OnNotice(msg)
	}
}

// Recap is this scene's running record and the last message it covers.
func (c *ChatView) Recap() (string, int64) { return c.recap, c.recapUpto }

// SetRecap stores a corrected record. The part of the scene it covers stays
// the same: what changes is what the model is told happened in it.
func (c *ChatView) SetRecap(text string) error {
	if c.chat.ID == 0 {
		return nil
	}
	text = strings.TrimSpace(text)
	if err := c.store.SetChatSummary(c.chat.ID, text, c.recapUpto); err != nil {
		return err
	}
	c.recap = text
	c.chat.Summary = text
	return nil
}

// Pins is this chat's pinned messages, oldest first.
func (c *ChatView) Pins() []store.Moment {
	if c.chat.ID == 0 {
		return nil
	}
	pins, err := c.store.Pinned(c.chat.ID, 0)
	if err != nil {
		c.fail("Could not read the pinned messages: " + err.Error())
	}
	return pins
}

// Unpin takes the pin off a message, and off its row if it is on screen.
func (c *ChatView) Unpin(id int64) error {
	if err := c.store.SetMessagePinned(id, false); err != nil {
		return err
	}
	for _, r := range c.rows {
		if r.ID == id {
			r.SetPinned(false)
		}
	}
	for i := range c.older {
		if c.older[i].ID == id {
			c.older[i].Pinned = false
		}
	}
	return nil
}

// SpeakerName is who wrote a stored message, for listing it.
func (c *ChatView) SpeakerName(role string, characterID int64) string {
	if role == ollama.RoleUser {
		return c.youName()
	}
	if characterID != 0 {
		if n := c.nameOf(characterID); n != "" {
			return n
		}
	}
	return c.char.Name
}

// memoryChip opens the scene's memory: its record and what is pinned.
func (c *ChatView) memoryChip() *gtk.Button {
	btn := gtk.NewButtonWithLabel("Memory")
	btn.AddCSSClass("chat-action-chip")
	btn.SetTooltipText("See and correct what this scene remembers")
	btn.ConnectClicked(func() {
		if c.OnEditMemory != nil {
			c.OnEditMemory()
		}
	})
	return btn
}

// turnChip is a group scene's "who is next": somebody chosen to answer what you
// write, or the cast carrying on without you.
func (c *ChatView) turnChip() *gtk.MenuButton {
	btn := gtk.NewMenuButton()
	btn.SetLabel("Who Answers")
	btn.AddCSSClass("chat-action-chip")
	btn.SetTooltipText("Choose who answers next, or let them talk without you")

	box := gtk.NewBox(gtk.OrientationVertical, 2)
	pop := gtk.NewPopover()
	item := func(label, tip string, fire func()) {
		b := gtk.NewButtonWithLabel(label)
		b.AddCSSClass("flat")
		b.SetTooltipText(tip)
		gtk.BaseWidget(b.Child()).SetHAlign(gtk.AlignStart)
		b.ConnectClicked(func() {
			pop.Popdown()
			fire()
		})
		box.Append(b)
	}
	item("Let Them Talk", "The characters carry on among themselves, without you", func() { c.Speak("") })
	for _, name := range c.castNames() {
		name := name
		item(name+" Answers", "Send your message with "+name+" answering, or have them speak now", func() { c.Speak(name) })
	}
	pop.SetChild(box)
	btn.SetPopover(pop)
	return btn
}
