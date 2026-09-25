package ui

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
	"astral/internal/world"
)

// flushInterval is how often streamed tokens are drained into the label.
//
// The obvious implementation — hop to the main thread once per token — costs
// an idle source and a full relayout per token, and since each update rewrites
// the whole label it is quadratic in the length of the reply. A local model
// emitting 60 tokens a second turns that into real jank halfway through a long
// roleplay response. Buffering into a builder and flushing on a timer makes
// the cost proportional to elapsed time instead of to token count, and 50ms is
// fast enough that it still reads as typing.
const flushInterval = 50

// transcriptMaxWidth caps the column the conversation sits in, so a maximised
// window does not stretch it across the whole screen.
// transcriptMaxWidth caps the column the conversation sits in, so a maximised
// window on a large display does not stretch it across the whole screen. Like
// the bubble constants it is an input to GTK's negotiation rather than the
// resulting width: 950 produces a column of about 1400px on a 4K screen.
const transcriptMaxWidth = 950

// renderWindow is how many messages are built as widgets when a chat opens.
//
// A row costs about 0.2ms to construct, measured, so a 500-message scene took
// over 100ms to put on screen and a long-running one would keep getting worse.
// Rendering only the tail bounds that: opening any chat costs the same as
// opening a short one, and the rest is built on request.
//
// Nothing is hidden from the model by this. The context sent on each turn is
// read from the database (see history), not from the rows on screen.
const renderWindow = 120

// learnTimeout bounds the background lorebook pass. Like compaction it is not
// blocking anything, so it can afford to be patient.
const learnTimeout = 5 * time.Minute

// compactTimeout bounds the background summarization. Generous: it is not
// blocking anything, and a large model folding twenty turns into a record has
// real work to do.
const compactTimeout = 5 * time.Minute

// maxComposerHeight caps how tall the input grows before it scrolls, in pixels.
const maxComposerHeight = 220

// ChatView is the centre panel: a transcript and the composer under it.
type ChatView struct {
	client *ollama.Client
	store  *store.Store

	widget    *gtk.Box
	scroll    *gtk.ScrolledWindow
	clamp     *adw.Clamp
	column    *gtk.Box
	composer  *gtk.TextView
	actionBar *gtk.Box
	sendBtn   *gtk.Button
	modelBtn  *gtk.Button
	hint      *gtk.Label

	chat store.Chat
	char chars.Character
	cfg  store.Config
	// mode is how message bodies are rendered. Roleplay prose has its own
	// visual grammar; an assistant answer written in it would have half its
	// emphasis silently dimmed, so the mode follows the kind of chat.
	mode Prose

	rows []*MessageRow

	// Streaming state. Everything here is touched only from the GTK main
	// thread except the pending buffers, which have their own lock.
	busy   bool
	cancel context.CancelFunc
	live   *MessageRow
	// greeting is the character's opening message while it is still unsaved,
	// so the first send can persist it instead of adding a second copy.
	greeting *MessageRow
	// gen increments whenever the view moves to a different chat. A reply that
	// completes after you have navigated away carries a stale gen and is
	// discarded, instead of being appended to whatever is on screen now.
	gen       int
	streamGen int

	scrollPending bool

	// attachPath is an image queued for the next message, and attachBtn is
	// the control that queues it. Only a design chat offers this: a vision
	// model reading a reference picture is how a description gets written
	// from something you have rather than something you can describe.
	attachPath string
	attachBtn  *gtk.Button
	attachChip *gtk.Box
	attachName *gtk.Label
	// lastImage is the most recent image sent in this chat, offered as the
	// character's portrait when the card is built.
	lastImage string
	// pendingImage is the base64 of an attachment waiting to go out with the
	// turn that is being assembled.
	pendingImage string

	// recap is the running record of everything compacted out of this chat's
	// context, and recapUpto is the last message id it covers. Turns newer
	// than that are still sent word-for-word.
	recap      string
	recapUpto  int64
	compacting bool

	// The setting this scene takes place in, and its lorebook. Loaded once
	// when a chat opens rather than per turn: a lorebook is small enough to
	// hold, and matching against it is a string scan rather than a query.
	world    world.World
	lore     []world.Entry
	learning bool

	// older holds the part of a long transcript that has not been built as
	// widgets yet, newest last. See renderWindow.
	older      []store.Message
	earlierBtn *gtk.Button

	pendMu    sync.Mutex
	pendText  strings.Builder
	pendThink strings.Builder
	flushID   coreglib.SourceHandle

	// OnChatChanged asks the sidebar to refresh (title or ordering changed).
	OnChatChanged func()
	// OnError surfaces a failure as a toast.
	OnError func(string)
	// OnPickModel is invoked when the composer's model chip is clicked.
	OnPickModel func()
	// OnBuildCharacter is invoked by the designer chat's action chip.
	OnBuildCharacter func()
	// OnBuildStyle is the same for a writing-style design chat.
	OnBuildStyle func()
	// OnAttachImage asks the app to choose an image. The app calls
	// AttachImage with the result.
	OnAttachImage func()
	// OnLoreLearned reports a finished learning pass: how many entries were
	// applied, and how many were held back for review.
	OnLoreLearned func(applied, held int)
}

// NewChatView builds the centre panel.
func NewChatView(client *ollama.Client, st *store.Store, cfg store.Config) *ChatView {
	c := &ChatView{client: client, store: st, cfg: cfg, mode: Roleplay}

	c.widget = gtk.NewBox(gtk.OrientationVertical, 0)
	c.widget.AddCSSClass("chat-view")
	c.widget.SetVExpand(true)

	// Transcript. The column is centred and width-capped so long prose keeps a
	// readable measure on a wide window.
	c.column = gtk.NewBox(gtk.OrientationVertical, 0)
	c.column.AddCSSClass("chat-column")

	// AdwClamp rather than a size request. A size request is a *minimum*, so
	// the old fixed 720 meant a narrow window could not shrink the column and
	// content was clipped instead of reflowing. A clamp sets a maximum and
	// gives way below it, which is the behaviour actually wanted.
	c.clamp = adw.NewClamp()
	c.clamp.SetMaximumSize(transcriptMaxWidth)
	c.clamp.SetTighteningThreshold(600)
	c.clamp.SetChild(c.column)
	c.clamp.SetHExpand(true)

	c.scroll = gtk.NewScrolledWindow()
	c.scroll.SetChild(c.clamp)
	c.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	c.scroll.SetVExpand(true)
	c.widget.Append(c.scroll)

	c.widget.Append(c.buildComposer())
	return c
}

// Widget returns the panel's root widget.
func (c *ChatView) Widget() gtk.Widgetter { return c.widget }

// SetConfig updates the sampling and display settings used for the next turn.
func (c *ChatView) SetConfig(cfg store.Config) {
	c.cfg = cfg
	c.refreshModelChip()
}

func (c *ChatView) buildComposer() *gtk.Widget {
	wrap := gtk.NewBox(gtk.OrientationVertical, 0)
	wrap.AddCSSClass("composer-wrap")

	// Actions offered by the current kind of chat, directly above the input
	// where they are in the way of nothing and still impossible to miss.
	// The queued attachment, shown above the input so it is impossible to send
	// an image by accident or to forget one is waiting.
	c.attachChip = gtk.NewBox(gtk.OrientationHorizontal, 6)
	c.attachChip.AddCSSClass("attach-chip")
	c.attachChip.SetHAlign(gtk.AlignCenter)
	c.attachChip.SetVisible(false)
	c.attachName = gtk.NewLabel("")
	c.attachName.SetEllipsize(3)
	c.attachChip.Append(gtk.NewImageFromIconName(IconFolder))
	c.attachChip.Append(c.attachName)
	drop := gtk.NewButtonFromIconName(IconTrash)
	drop.AddCSSClass("message-action")
	drop.SetTooltipText("Remove the attached image")
	drop.ConnectClicked(func() { c.AttachImage("") })
	c.attachChip.Append(drop)
	wrap.Append(c.attachChip)

	c.actionBar = gtk.NewBox(gtk.OrientationHorizontal, 6)
	c.actionBar.AddCSSClass("chat-actions")
	c.actionBar.SetHAlign(gtk.AlignCenter)
	c.actionBar.SetVisible(false)
	wrap.Append(c.actionBar)

	card := gtk.NewBox(gtk.OrientationVertical, 4)
	card.AddCSSClass("composer")

	c.composer = gtk.NewTextView()
	c.composer.SetWrapMode(gtk.WrapWordChar)
	c.composer.SetAcceptsTab(false) // Tab should move focus, not indent a message
	// No top/bottom margin here: the stylesheet already pads the text view,
	// and carrying both made an empty composer about twice as tall as the one
	// line it contains.

	// The input grows with its content and then scrolls, rather than pushing
	// the transcript off the top of the window.
	inputScroll := gtk.NewScrolledWindow()
	inputScroll.SetChild(c.composer)
	inputScroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	inputScroll.SetMaxContentHeight(maxComposerHeight)
	inputScroll.SetPropagateNaturalHeight(true)
	card.Append(inputScroll)

	tools := gtk.NewBox(gtk.OrientationHorizontal, 6)
	tools.AddCSSClass("composer-tools")

	c.attachBtn = gtk.NewButtonFromIconName(IconFolder)
	c.attachBtn.AddCSSClass("composer-model")
	c.attachBtn.SetTooltipText("Attach a reference image for the model to look at")
	c.attachBtn.SetVisible(false)
	c.attachBtn.ConnectClicked(func() {
		if c.OnAttachImage != nil {
			c.OnAttachImage()
		}
	})
	tools.Append(c.attachBtn)

	c.modelBtn = gtk.NewButton()
	c.modelBtn.AddCSSClass("composer-model")
	c.modelBtn.SetTooltipText("Choose the model for this chat")
	c.modelBtn.ConnectClicked(func() {
		if c.OnPickModel != nil {
			c.OnPickModel()
		}
	})
	tools.Append(c.modelBtn)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	tools.Append(spacer)

	c.sendBtn = gtk.NewButtonFromIconName(IconSend)
	c.sendBtn.AddCSSClass("send-button")
	c.sendBtn.SetTooltipText("Send (Enter)")
	c.sendBtn.SetSensitive(false)
	c.sendBtn.ConnectClicked(c.onSendClicked)
	tools.Append(c.sendBtn)

	card.Append(tools)

	composerClamp := adw.NewClamp()
	composerClamp.SetMaximumSize(760)
	composerClamp.SetTighteningThreshold(600)
	composerClamp.SetChild(card)
	wrap.Append(composerClamp)

	c.hint = gtk.NewLabel("")
	c.hint.AddCSSClass("composer-hint")
	c.hint.SetHAlign(gtk.AlignCenter)
	c.hint.SetVisible(false)
	wrap.Append(c.hint)

	// Enter sends; Shift+Enter is a newline. Handled on the key controller so
	// it runs before the text view inserts the character.
	key := gtk.NewEventControllerKey()
	key.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
		if keyval != gdk.KEY_Return && keyval != gdk.KEY_KP_Enter {
			return false
		}
		if state&gdk.ShiftMask != 0 {
			return false // let it insert a newline
		}
		c.onSendClicked()
		return true
	})
	c.composer.AddController(key)

	// Send is only live when there is something to send, so the button's state
	// answers "will this do anything?" without having to try it.
	c.composer.Buffer().ConnectChanged(func() {
		c.sendBtn.SetSensitive(c.busy || strings.TrimSpace(c.composerText()) != "")
	})

	c.refreshModelChip()
	return &wrap.Widget
}

func (c *ChatView) composerText() string {
	b := c.composer.Buffer()
	start, end := b.Bounds()
	return b.Text(start, end, false)
}

func (c *ChatView) setComposerText(s string) {
	c.composer.Buffer().SetText(s)
}

func (c *ChatView) refreshModelChip() {
	if c.modelBtn == nil {
		return
	}
	m := c.activeModel()
	if m == "" {
		m = "Choose a model"
	}
	c.modelBtn.SetLabel(m)
}

// activeModel is the chat's own model if it has one, else the global default.
// A chat remembers the model it was started with, so reopening an old scene
// does not silently continue it in a different voice.
func (c *ChatView) activeModel() string {
	if c.chat.Model != "" {
		return c.chat.Model
	}
	return c.cfg.Model
}

// Chat returns the conversation currently displayed.
func (c *ChatView) Chat() store.Chat { return c.chat }

// Clear empties the transcript and invalidates any in-flight reply.
func (c *ChatView) Clear() {
	c.Stop()
	// Bumping the generation is what makes an in-flight reply land nowhere:
	// see the staleness guard in finishStream.
	c.gen++
	for _, r := range c.rows {
		c.column.Remove(r.Widget())
	}
	c.rows = nil
	c.live = nil
	c.greeting = nil
	c.older = nil
	if c.earlierBtn != nil {
		c.column.Remove(c.earlierBtn)
		c.earlierBtn = nil
	}
	c.chat = store.Chat{}
	c.char = chars.Character{}
	c.recap, c.recapUpto = "", 0
	c.world, c.lore = world.World{}, nil
}

// LoadChat displays a conversation and its character.
func (c *ChatView) LoadChat(ch store.Chat, ca chars.Character, msgs []store.Message) {
	c.Clear()
	c.chat, c.char = ch, ca
	c.recap, c.recapUpto = ch.Summary, ch.SummaryUpto
	c.loadLore(ca)
	c.mode = Roleplay
	switch ch.Kind {
	case store.KindDesigner, store.KindAssistant, store.KindStyleDesigner:
		c.mode = Plain
	}
	c.refreshModelChip()
	c.refreshActions()

	// Only the tail is built; the rest waits behind the button below.
	if len(msgs) > renderWindow {
		c.older = msgs[:len(msgs)-renderWindow]
		msgs = msgs[len(msgs)-renderWindow:]
	}
	c.refreshEarlierButton()
	for _, m := range msgs {
		row := c.appendRow(m.Role, m.Content, m.Thinking, m.ID, m.CreatedAt)
		if m.TokPerSec > 0 && c.cfg.ShowStats {
			row.SetMeta(ollama.Stats{Tokens: m.EvalCount, TokPerSec: m.TokPerSec}.Summary())
		}
	}
	c.scrollToBottom()
	c.focusComposer()
}

// FocusComposer puts the cursor in the input.
func (c *ChatView) FocusComposer() { c.focusComposer() }

func (c *ChatView) focusComposer() {
	if c.composer != nil {
		c.composer.GrabFocus()
	}
}

// speaker returns the display name, initial and tint for a role.
func (c *ChatView) speaker(role string) (string, string, int) {
	if role == ollama.RoleUser {
		name := c.cfg.PersonaName
		if name == "" {
			name = chars.DefaultPersonaName
		}
		return name, firstLetter(name), 0
	}
	if name := c.char.Name; name != "" {
		return name, c.char.Initial(), c.char.Accent
	}
	switch c.chat.Kind {
	case store.KindDesigner:
		return "Character designer", "✦", 1
	case store.KindStyleDesigner:
		return "Style designer", "✦", 3
	default:
		return "Assistant", "✦", 0
	}
}

func firstLetter(s string) string {
	for _, r := range s {
		if r != ' ' {
			return strings.ToUpper(string(r))
		}
	}
	return "?"
}

// appendRow adds a turn to the transcript.
func (c *ChatView) appendRow(role, text, thinking string, id int64, when time.Time) *MessageRow {
	// A run of messages from one speaker reads as a single turn in the
	// conversation, so only the first carries a name and an avatar.
	row := c.newRow(role, text, thinking, id, when, c.lastRole() == role)
	c.column.Append(row.Widget())
	c.rows = append(c.rows, row)
	return row
}

// newRow builds a row without placing it, so older batches can be inserted
// above the transcript rather than appended to it.
func (c *ChatView) newRow(role, text, thinking string, id int64, when time.Time, grouped bool) *MessageRow {
	name, initial, accent := c.speaker(role)
	opts := MessageOpts{
		Role:        role,
		DisplayName: name,
		Initial:     initial,
		Accent:      accent,
		Mode:        c.mode,
		Grouped:     grouped,
		When:        when,
	}
	// A fresh widget per row: a GtkPicture cannot be parented twice, so the
	// image is loaded again rather than shared. It is cheap, and GTK caches
	// the decoded texture behind the filename.
	if role != ollama.RoleUser && !grouped && c.char.AvatarPath != "" {
		opts.Avatar = NewCharacterAvatar(c.char, avatarSize)
	}
	row := NewMessageRow(opts)
	row.ID = id
	row.SetMarkdown(text)
	row.SetThinking(thinking)
	c.attachActions(row)
	return row
}

// attachActions adds the per-message hover buttons.
func (c *ChatView) attachActions(row *MessageRow) {
	row.AddAction(IconCopy, "Copy this message", func() {
		if d := gdk.DisplayGetDefault(); d != nil {
			d.Clipboard().SetText(row.Text())
		}
	})
	if row.Role == ollama.RoleAssistant {
		row.AddAction(IconRegenerate, "Write this reply again", func() {
			c.regenerate(row)
		})
	}
	row.AddAction(IconTrash, "Delete this message", func() {
		c.deleteRow(row)
	})
}

// atBottom reports whether the transcript is scrolled to the end. Used to
// decide whether new content should pull the view down: yanking someone back
// to the bottom while they are reading earlier in the scene is the single most
// irritating thing a chat window can do.
func (c *ChatView) atBottom() bool {
	adj := c.scroll.VAdjustment()
	return adj.Value() >= adj.Upper()-adj.PageSize()-64
}

// streamWidth is the width a streaming bubble is pinned to: as wide as the
// column allows, but never past the usual bubble cap. Pinning it to the
// maximum means a long reply — which is most of them — never reflows at all,
// and a short one resizes exactly once, when it finishes.
func (c *ChatView) streamWidth() int {
	avail := c.column.Width() - avatarSize - 60 // avatar, spacing, bubble padding
	if avail > bubbleMaxWidth {
		avail = bubbleMaxWidth
	}
	if avail < 160 {
		return 0 // too narrow to be worth pinning; let it negotiate
	}
	return avail
}

// refreshEarlierButton puts the "load earlier" control at the top of the
// transcript, or removes it once everything has been built.
func (c *ChatView) refreshEarlierButton() {
	if len(c.older) == 0 {
		if c.earlierBtn != nil {
			c.column.Remove(c.earlierBtn)
			c.earlierBtn = nil
		}
		return
	}
	if c.earlierBtn == nil {
		c.earlierBtn = gtk.NewButton()
		c.earlierBtn.AddCSSClass("sidebar-item")
		c.earlierBtn.SetHAlign(gtk.AlignCenter)
		c.earlierBtn.SetMarginBottom(12)
		c.earlierBtn.ConnectClicked(c.loadEarlier)
		c.column.Prepend(c.earlierBtn)
	}
	c.earlierBtn.SetLabel(fmt.Sprintf("Show %d earlier messages", min(len(c.older), renderWindow)))
}

// loadEarlier builds the next batch of older messages above what is already
// on screen.
func (c *ChatView) loadEarlier() {
	n := min(len(c.older), renderWindow)
	batch := c.older[len(c.older)-n:]
	c.older = c.older[:len(c.older)-n]

	// Built in reverse and prepended, so each ends up above the last. Grouping
	// is decided within the batch: the row below a batch is already on screen
	// and its own grouping was settled when it was built.
	rows := make([]*MessageRow, 0, len(batch))
	for i, m := range batch {
		grouped := i > 0 && batch[i-1].Role == m.Role
		row := c.newRow(m.Role, m.Content, m.Thinking, m.ID, m.CreatedAt, grouped)
		if m.TokPerSec > 0 && c.cfg.ShowStats {
			row.SetMeta(ollama.Stats{Tokens: m.EvalCount, TokPerSec: m.TokPerSec}.Summary())
		}
		rows = append(rows, row)
	}
	for i := len(rows) - 1; i >= 0; i-- {
		c.column.InsertChildAfter(rows[i].Widget(), c.earlierBtn)
	}
	c.rows = append(rows, c.rows...)
	c.refreshEarlierButton()
}

// lastRole is who spoke in the row currently at the bottom of the transcript.
func (c *ChatView) lastRole() string {
	if len(c.rows) == 0 {
		return ""
	}
	return c.rows[len(c.rows)-1].Role
}

func (c *ChatView) scrollToBottom() {
	// Deferred to an idle callback: the adjustment's upper bound is only
	// correct once GTK has laid out the row that was just added.
	//
	// Coalesced, because streaming calls this on every flush — twenty times a
	// second for the length of a reply — and each call is a closure plus an
	// idle source that gotk4 keeps alive for the life of the process. One
	// pending scroll does the same job.
	if c.scrollPending {
		return
	}
	c.scrollPending = true
	coreglib.IdleAdd(func() bool {
		c.scrollPending = false
		adj := c.scroll.VAdjustment()
		adj.SetValue(adj.Upper() - adj.PageSize())
		return false
	})
}

// SetClient swaps the Ollama client, after the server address is changed in
// settings. An in-flight reply keeps the client it started with — cancelling
// someone's generation because they edited an unrelated field would be its own
// kind of bug.
func (c *ChatView) SetClient(client *ollama.Client) {
	if client != nil {
		c.client = client
	}
}

// SetModel changes the model for the open chat.
func (c *ChatView) SetModel(model string) {
	c.chat.Model = model
	c.refreshModelChip()
}

// refreshActions rebuilds the chip row for the current kind of chat.
func (c *ChatView) refreshActions() {
	if c.actionBar == nil {
		return
	}
	for {
		child := c.actionBar.FirstChild()
		if child == nil {
			break
		}
		c.actionBar.Remove(child)
	}
	var label, tip string
	var fire func()
	switch c.chat.Kind {
	case store.KindDesigner:
		label, tip = "Create character", "Turn this conversation into a character you can play with"
		fire = func() {
			if c.OnBuildCharacter != nil {
				c.OnBuildCharacter()
			}
		}
	case store.KindStyleDesigner:
		label, tip = "Create style", "Turn this conversation into a writing style"
		fire = func() {
			if c.OnBuildStyle != nil {
				c.OnBuildStyle()
			}
		}
	default:
		c.actionBar.SetVisible(false)
		return
	}
	btn := gtk.NewButtonWithLabel(label)
	btn.AddCSSClass("chat-action-chip")
	btn.SetTooltipText(tip)
	btn.ConnectClicked(fire)
	c.actionBar.Append(btn)
	c.actionBar.SetVisible(true)
}

// History returns the conversation so far, for the designers' extraction step.
func (c *ChatView) History() []ollama.Message { return c.history() }

// SetBuilding shows that a designer's extraction call is running. It is a
// blocking, unstreamed request that can take a while on a large model, so the
// chip has to say something is happening rather than appearing to do nothing.
func (c *ChatView) SetBuilding(building bool) {
	if c.actionBar == nil {
		return
	}
	child := c.actionBar.FirstChild()
	if child == nil {
		return
	}
	btn, ok := child.(*gtk.Button)
	if !ok {
		return
	}
	if building {
		btn.SetLabel("Building…")
		btn.SetSensitive(false)
	} else {
		c.refreshActions() // restores the label this kind of chat uses
	}
}

// DevMeasure reports the laid-out geometry of the transcript, so a dev run can
// verify that messages wrap inside the window instead of overflowing it.
func (c *ChatView) DevMeasure() (viewWidth, columnWidth int, bubbles []int) {
	log.Printf("astral: measure:   composer textview = %dpx for one empty line",
		gtk.BaseWidget(c.composer).Height())
	viewWidth = gtk.BaseWidget(c.widget).Width()
	columnWidth = c.column.Width()
	log.Printf("astral: measure:   clamp width=%dpx max=%dpx threshold=%dpx",
		gtk.BaseWidget(c.clamp).Width(), c.clamp.MaximumSize(), c.clamp.TighteningThreshold())
	for i, r := range c.rows {
		bubbles = append(bubbles, r.BubbleWidth())
		h, lines := r.DevLineMetrics()
		per := 0.0
		if lines > 0 {
			per = float64(h) / float64(lines)
		}
		log.Printf("astral: measure:   row %d: body %dpx over %d lines = %.1fpx per line",
			i, h, lines, per)
	}
	return viewWidth, columnWidth, bubbles
}

// DevRowCount reports how many rows are built and how many are still waiting
// behind the "show earlier" button.
func (c *ChatView) DevRowCount() (built, pending int) { return len(c.rows), len(c.older) }

// DevLoadEarlier builds the next older batch, so the dev harness can exercise
// the path a click takes without a click.
func (c *ChatView) DevLoadEarlier() { c.loadEarlier() }

// DevShowTyping puts a row into the streaming state, so the dev harness can
// check the typing indicator draws without waiting on a real model.
func (c *ChatView) DevShowTyping() {
	row := c.appendRow(ollama.RoleAssistant, "", "", 0, time.Now())
	row.BeginStreaming(c.streamWidth())
	c.scrollToBottom()
}

// AttachImage queues an image to go with the next message, or clears the queue
// when path is empty.
func (c *ChatView) AttachImage(path string) {
	c.attachPath = path
	if c.attachChip == nil {
		return
	}
	if path == "" {
		c.attachChip.SetVisible(false)
		return
	}
	c.attachName.SetText(filepath.Base(path))
	c.attachChip.SetVisible(true)
}

// LastImage is the most recent image sent in this chat, if any.
func (c *ChatView) LastImage() string { return c.lastImage }

// SetCanAttachImages shows or hides the attach control. The app decides, since
// it is the one that knows whether the model can see.
func (c *ChatView) SetCanAttachImages(can bool) {
	if c.attachBtn != nil {
		c.attachBtn.SetVisible(can)
	}
	if !can {
		c.AttachImage("")
	}
}

// loadLore reads the character's world and its lorebook.
func (c *ChatView) loadLore(ca chars.Character) {
	c.world, c.lore = world.World{}, nil
	if ca.WorldID == 0 || c.store == nil {
		return
	}
	w, err := c.store.World(ca.WorldID)
	if err != nil {
		// A world deleted out from under a character is not an error worth
		// interrupting a scene for: it simply has no setting any more.
		return
	}
	entries, err := c.store.LoreEntries(ca.WorldID)
	if err != nil {
		log.Printf("astral: reading lore for world %d: %v", ca.WorldID, err)
		return
	}
	c.world, c.lore = w, entries
}

// loreFor renders the lore this part of the conversation has triggered.
func (c *ChatView) loreFor(history []ollama.Message) string {
	if len(c.lore) == 0 {
		return ""
	}
	turns := make([]string, 0, len(history))
	for _, m := range history {
		turns = append(turns, m.Content)
	}
	// The character's own description is scanned too. A scene that has only
	// just opened has almost no transcript, and without this the setting would
	// not appear until someone happened to name part of it out loud.
	turns = append([]string{c.char.Description + " " + c.char.Scenario}, turns...)
	return world.Render(c.world, world.Match(c.lore, world.RecentText(turns), world.BudgetChars))
}

// Lore exposes the loaded lorebook, for the auto-update pass.
func (c *ChatView) Lore() (world.World, []world.Entry) { return c.world, c.lore }

// SetLore replaces the loaded lorebook after the model has updated it.
func (c *ChatView) SetLore(entries []world.Entry) { c.lore = entries }
