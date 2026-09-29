package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
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

// toggleHidden hides a message from the model, or shows it again. It stays
// in the transcript either way.
func (c *ChatView) toggleHidden(row *MessageRow) {
	if c.busy {
		c.fail("Wait for the reply to finish first.")
		return
	}
	if row.ID == 0 {
		c.fail("Send a message first: there is nothing stored to hide yet.")
		return
	}
	hide := !row.Hidden
	if err := c.store.SetMessageHidden(row.ID, hide); err != nil {
		c.fail("Could not change that: " + err.Error())
		return
	}
	row.SetHidden(hide)
	if hide {
		c.notice("Hidden. The model no longer sees it; it stays here for you.")
	} else {
		c.notice("The model sees it again.")
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
	if c.ideasBtn != nil {
		c.ideasBtn.SetVisible(c.canDraft())
		c.ideasBtn.SetSensitive(!c.busy)
	}
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
		chat := func(ctx context.Context, m []ollama.Message, d func(ollama.Delta)) (ollama.Message, ollama.Stats, error) {
			return client.Chat(ctx, model, m, opts, &noThink, d)
		}
		// Through the stock phrase filter: your turn deserves it as much as
		// the character's.
		msg, _, err := scene.Unslop(ctx, chat, msgs, func(d ollama.Delta) {
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
		chat := func(ctx context.Context, m []ollama.Message, d func(ollama.Delta)) (ollama.Message, ollama.Stats, error) {
			return client.Chat(ctx, model, m, opts, &noThink, d)
		}
		_, _, err := scene.Unslop(ctx, chat, msgs, func(d ollama.Delta) {
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

// warmKey names this conversation to the warm-up, which keeps track of the
// chat each model last read.
func (c *ChatView) warmKey() string {
	if c.chat.ID != 0 {
		return strconv.FormatInt(c.chat.ID, 10)
	}
	return "new " + c.char.Name
}

// warmForTyping starts the model and the scene's prompt on their way while a
// message is typed. See scene.WarmForTyping.
func (c *ChatView) warmForTyping() {
	model := c.activeModel()
	if model == "" {
		return
	}
	msgs := c.buildRequest()
	client, kind, opts, key := c.client, c.chat.Kind, c.options(), c.warmKey()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		// The same context size the reply will ask for, or the model is
		// loaded twice.
		opts = scene.FitContext(ctx, client, model, kind, opts, msgs)
		cancel()
		scene.WarmForTyping(client, model, key, msgs, opts)
	}()
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

// chipLabel is a chip's text as a label that shortens with an ellipsis.
//
// A button's own label never shortens, so its whole text is the narrowest the
// button can be, and a direction running to sixty characters made the chat
// alone too wide for a window under about a thousand pixels: the window could
// be dragged narrower than its content and drew the rest as a black strip.
func chipLabel(text string, maxChars int) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetEllipsize(pango.EllipsizeEnd)
	l.SetMaxWidthChars(maxChars)
	return l
}

// memoryChip opens the scene's memory: its record and what is pinned.
func (c *ChatView) memoryChip() *gtk.Button {
	btn := gtk.NewButton()
	btn.SetChild(chipLabel("Memory", 12))
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
	btn.SetChild(chipLabel("Who Answers", 14))
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

// Setting is where and when this scene is now, in a line, and whether Astral
// keeps it up to date.
func (c *ChatView) Setting() (string, bool) { return c.chat.Setting, c.chat.SettingAuto }

// SetSetting stores where and when the scene is now, and whether Astral keeps
// it up to date from here. It is sent from the next turn on.
func (c *ChatView) SetSetting(setting string, auto bool) error {
	setting = strings.TrimSpace(setting)
	c.chat.Setting, c.chat.SettingAuto = setting, auto
	if c.chat.ID == 0 {
		return nil
	}
	if err := c.store.SetChatSettingAuto(c.chat.ID, auto); err != nil {
		return err
	}
	return c.store.SetChatSetting(c.chat.ID, setting)
}

// maybeTrackSetting brings the scene's setting line up to date after a
// reply, in the background lane, when Astral is keeping it. See
// scene.TrackSetting.
func (c *ChatView) maybeTrackSetting() {
	if c.bg.running || c.chat.ID == 0 || !c.chat.SettingAuto || !c.canDraft() {
		return
	}
	ctx, ok := c.bg.take("setting", 90*time.Second)
	if !ok {
		return
	}
	chatID := c.chat.ID
	client, model := c.client, c.activeModel()
	st, cfg, ch, cast, hist := c.store, c.cfg, c.chat, c.sceneCast(), c.history()
	go func() {
		line := scene.TrackSetting(ctx, client, st, cfg, ch, cast, hist, model)
		coreglib.IdleAdd(func() bool {
			c.bg.done()
			// Only onto the chat it was worked out for, and only if nobody
			// wrote their own in the meantime.
			if line == "" || c.chat.ID != chatID || !c.chat.SettingAuto {
				return false
			}
			if err := c.store.SetChatSetting(chatID, line); err != nil {
				return false
			}
			c.chat.Setting = line
			return false
		})
	}()
}

// Usage is how full the model's memory is on the next turn, and what with.
func (c *ChatView) Usage() scene.Usage {
	return scene.MeasureUsage(c.store, c.cfg, c.chat, c.sceneCast(), c.history())
}

// Busy reports whether a reply or a draft is being written in this chat.
func (c *ChatView) Busy() bool { return c.busy || c.drafting }

// Seen is what the next turn sends besides the conversation: the lorebook
// entries and why, and the earlier moments. See scene.WhatItSees.
func (c *ChatView) Seen() scene.Seen {
	return scene.WhatItSees(c.store, c.cfg, c.chat, c.sceneCast(), c.history())
}

// SuggestSetting asks the model where and when the scene is now, and hands
// the line to done on the UI thread. Nothing is stored.
func (c *ChatView) SuggestSetting(done func(string, error)) {
	if c.busy || c.drafting {
		done("", fmt.Errorf("wait for the reply to finish"))
		return
	}
	model := c.activeModel()
	if model == "" {
		done("", fmt.Errorf("choose a model first"))
		return
	}
	msgs := scene.SuggestSetting(c.store, c.cfg, c.chat, c.sceneCast(), c.history())
	opts := scene.DraftOptions(c.cfg, c.chat.Kind)
	client := c.client
	c.drafting = true
	c.refreshDraftButton()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		client.UseForReplies(ctx, model)
		noThink := false
		msg, _, err := client.Chat(ctx, model, msgs, opts, &noThink, nil)
		_, text := ollama.SplitThinking(msg.Content)
		coreglib.IdleAdd(func() {
			c.drafting = false
			c.refreshDraftButton()
			done(chars.CleanSetting(text), err)
		})
	}()
}

// Suggest asks for three things you could say next, and hands them to done
// on the UI thread.
func (c *ChatView) Suggest(done func([]string, error)) {
	if c.busy || c.drafting {
		done(nil, fmt.Errorf("wait for the reply to finish"))
		return
	}
	model := c.activeModel()
	if model == "" {
		done(nil, fmt.Errorf("choose a model first"))
		return
	}
	msgs := scene.Suggest(c.store, c.cfg, c.chat, c.sceneCast(), c.history())
	opts := scene.SuggestOptions(c.cfg, c.chat.Kind)
	client := c.client
	userName := c.youName()
	c.drafting = true
	c.refreshDraftButton()
	c.sendBtn.SetSensitive(false)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		client.UseForReplies(ctx, model)
		raw, _, err := client.Structured(ctx, model, msgs, opts, chars.SuggestSchema)
		options := chars.ParseSuggestions(raw, userName)
		coreglib.IdleAdd(func() {
			c.drafting = false
			c.refreshDraftButton()
			c.sendBtn.SetSensitive(strings.TrimSpace(c.composerText()) != "")
			if err == nil && len(options) == 0 {
				err = fmt.Errorf("the model offered nothing usable, so try again")
			}
			done(options, err)
		})
	}()
}

// UseSuggestion puts a suggestion in the message box, to send or change.
func (c *ChatView) UseSuggestion(text string) {
	c.setComposerText(text)
	c.focusComposer()
	buf := c.composer.Buffer()
	buf.PlaceCursor(buf.EndIter())
}

// ideasButton offers three things you could say next, in a popover.
func (c *ChatView) ideasButton() *gtk.MenuButton {
	btn := gtk.NewMenuButton()
	btn.SetIconName(IconIdeas)
	btn.AddCSSClass("composer-model")
	btn.SetTooltipText("Suggest three things you could say next")
	pop := gtk.NewPopover()
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	box.SetMarginTop(6)
	box.SetMarginBottom(6)
	box.SetMarginStart(6)
	box.SetMarginEnd(6)
	pop.SetChild(box)
	btn.SetPopover(pop)
	clear := func() {
		for ch := box.FirstChild(); ch != nil; ch = box.FirstChild() {
			box.Remove(ch)
		}
	}
	say := func(text string) {
		l := gtk.NewLabel(text)
		l.SetWrap(true)
		l.SetMaxWidthChars(48)
		l.AddCSSClass("dim-label")
		box.Append(l)
	}
	pop.ConnectShow(func() {
		clear()
		say("Thinking of three…")
		c.Suggest(func(options []string, err error) {
			clear()
			if err != nil {
				say("Could not suggest anything: " + err.Error())
				return
			}
			for _, o := range options {
				o := o
				b := gtk.NewButton()
				b.AddCSSClass("flat")
				// Shown without its markup; what goes in the box keeps it.
				l := gtk.NewLabel(strings.ReplaceAll(o, "*", ""))
				l.SetWrap(true)
				l.SetMaxWidthChars(48)
				l.SetXAlign(0)
				b.SetChild(l)
				b.SetTooltipText("Put this in the message box")
				b.ConnectClicked(func() {
					pop.Popdown()
					c.UseSuggestion(o)
				})
				box.Append(b)
			}
		})
	})
	return btn
}
