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
	"github.com/diamondburned/gotk4/pkg/pango"

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

	widget      *gtk.Box
	scroll      *gtk.ScrolledWindow
	clamp       *adw.Clamp
	column      *gtk.Box
	composer    *gtk.TextView
	actionBar   *gtk.Box
	sendBtn     *gtk.Button
	modelBtn    *gtk.Button
	hint        *gtk.Label
	placeholder *gtk.Label

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
	// so the first send can persist it instead of adding a second copy, and
	// greetingAt is which of the character's openings is showing.
	greeting   *MessageRow
	greetingAt int
	// gen increments whenever the view moves to a different chat. A reply that
	// completes after you have navigated away carries a stale gen and is
	// discarded, instead of being appended to whatever is on screen now.
	gen       int
	streamGen int

	scrollPending bool
	// scrollTravel says the pending scroll should glide rather than snap.
	scrollTravel bool
	// scrollAnim glides the transcript to a new message. One object, re-aimed;
	// see motion.go.
	scrollAnim *adw.TimedAnimation
	// settled says the transcript on screen is the one that was stored, so a
	// row added from here is new and should arrive rather than appear.
	settled bool

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

	// prefilled records that this turn's request ended in a partial assistant
	// message, so the reply has to be joined back onto it. See
	// chars.NarrationPrefill.
	prefilled bool

	// continuing is the reply being extended, when a turn stopped at the token
	// limit and is being asked for the rest. Nil for an ordinary turn.
	continuing *MessageRow
	// continuePrefix is the reply as it stood before the continuation, so the
	// two halves can be joined when it finishes.
	continuePrefix string
	// continueSpeaker is who the continued reply belongs to. In a group the two
	// halves have to be rejoined under the right name: the rest of a reply
	// carries no label, because the model is finishing a sentence rather than
	// starting a turn.
	continueSpeaker int64

	// thinkStream keeps deliberation that arrives inside the reply off the
	// screen while it streams. See ollama.ThinkStream.
	thinkStream ollama.ThinkStream

	// collapsed records that this reply was stopped because the model came
	// apart, and collapseWhy is which way. See looping.go.
	collapsed   bool
	collapseWhy string

	// warnedSpill stops the "your housekeeping model did not fit" notice from
	// repeating: it is true of the configuration, not of the turn, so saying
	// it once is saying it.
	warnedSpill bool

	// recap is the running record of everything compacted out of this chat's
	// context, and recapUpto is the last message id it covers. Turns newer
	// than that are still sent word-for-word.
	recap     string
	recapUpto int64

	// The setting this scene takes place in, and its lorebook. Loaded once
	// when a chat opens rather than per turn: a lorebook is small enough to
	// hold, and matching against it is a string scan rather than a query.
	world world.World
	lore  []world.Entry

	// Background model work: compaction and the lorebook pass. They share one
	// lane, and the user's own turn takes it from them.
	//
	// They used to have a flag each, which meant both could be generating at
	// once — two large requests against one model, on top of whatever the user
	// did next. Ollama serves them in turn, so the visible effect was the next
	// reply waiting behind a recap the user never asked for and cannot see.
	bg bgWork

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
	// OnBuildWorld is the same for a world design chat.
	OnBuildWorld func()
	// OnAttachImage asks the app to choose an image. The app calls
	// AttachImage with the result.
	OnAttachImage func()
	// OnEditDirection is the direction chip being clicked. The dialog lives in
	// the app layer, like the other editors.
	OnEditDirection func()
	// OnEditCast is the cast chip being clicked, for changing who is in a scene.
	OnEditCast func()

	// cast is every character in this scene. One member, or none, is an
	// ordinary conversation and behaves exactly as it did before there were
	// groups. See chatview_cast.go.
	cast []chars.Character
	// spoken is everyone who has said something in this scene, which is not the
	// same list as the cast once somebody has been written out of it. The cast
	// is who can speak next; this is who a stored line can belong to.
	spoken []chars.Character
	// beats folds a group reply into its speakers as it streams, and liveRows
	// are the rows it is being streamed into, in order.
	beats    chars.BeatStream
	liveRows []*beatRow

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
	c.attachName.SetEllipsize(pango.EllipsizeEnd)
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

	// GtkTextView has no placeholder, so it gets one: a label laid over the
	// empty input and hidden the moment there is anything to hide it behind.
	// Without it the composer is a blank rectangle that never says what it
	// wants from you, which on a first run is the only question that matters.
	c.placeholder = gtk.NewLabel("")
	c.placeholder.AddCSSClass("composer-placeholder")
	c.placeholder.SetXAlign(0)
	c.placeholder.SetYAlign(0)
	c.placeholder.SetEllipsize(pango.EllipsizeEnd)
	c.placeholder.SetCanTarget(false) // clicks belong to the text view under it
	inputOverlay := gtk.NewOverlay()
	inputOverlay.SetChild(inputScroll)
	inputOverlay.AddOverlay(c.placeholder)
	card.Append(inputOverlay)

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
		c.placeholder.SetVisible(c.composerText() == "")
	})

	c.refreshPlaceholder()
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

// refreshPlaceholder says what this particular composer is for. A scene, a
// plain chat and a design session all want different things typed into them,
// and the composer is where you are looking when you wonder which.
func (c *ChatView) refreshPlaceholder() {
	if c.placeholder == nil {
		return
	}
	var text string
	switch {
	case c.char.Name != "":
		text = "Write your reply, or *describe what you do*"
	case c.chat.Kind == store.KindDesigner:
		text = "Describe who you want, in as much or as little detail as you like"
	case c.chat.Kind == store.KindStyleDesigner:
		text = "Describe how you want the writing to read"
	case c.chat.Kind == store.KindAssistant:
		text = "Ask anything"
	default:
		text = "Write a message"
	}
	c.placeholder.SetText(text)
	c.placeholder.SetVisible(c.composerText() == "")
}

func (c *ChatView) refreshModelChip() {
	if c.modelBtn == nil {
		return
	}
	m := c.activeModel()
	if m == "" {
		c.modelBtn.SetLabel("Choose a model")
		c.modelBtn.SetTooltipText("Choose the model for this chat")
		return
	}
	// The full tag is a path with a registry org in front of it, and at
	// composer size that is a wall of text sitting where a small control
	// should be. The short form is what distinguishes one of your models from
	// another; the whole thing stays a hover away.
	c.modelBtn.SetLabel(shortModel(m))
	c.modelBtn.SetTooltipText(m + "\nClick to use a different model for this chat")
}

// shortModel trims a model tag to the part that identifies it.
//
// Lengths here are in runes, not bytes. A tag is ASCII in practice, but the
// ellipsis is not, and counting it as one byte is how a 28-character budget
// quietly produces a 30-character label.
func shortModel(m string) string {
	if i := strings.LastIndexByte(m, '/'); i >= 0 && i+1 < len(m) {
		m = m[i+1:]
	}
	const max = 28
	r := []rune(m)
	if len(r) <= max {
		return m
	}
	// Keeping the tag is the point: qwen3:8b and qwen3:32b differ only at the
	// end, so that is the end that survives.
	if i := strings.LastIndexByte(m, ':'); i > 0 {
		tail := []rune(m[i:])
		if len(tail) < max-1 {
			return string(r[:max-len(tail)-1]) + "…" + string(tail)
		}
	}
	return string(r[:max-1]) + "…"
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
	c.settled = false
	// A glide aimed at the chat being left would carry on moving the view while
	// the next one is being built.
	c.stopGlide()
	c.older = nil
	if c.earlierBtn != nil {
		c.column.Remove(c.earlierBtn)
		c.earlierBtn = nil
	}
	c.chat = store.Chat{}
	c.char = chars.Character{}
	c.cast, c.spoken = nil, nil
	c.clearBeats()
	c.recap, c.recapUpto = "", 0
	c.world, c.lore = world.World{}, nil
}

// LoadChat displays a conversation and its character.
func (c *ChatView) LoadChat(ch store.Chat, ca chars.Character, msgs []store.Message) {
	c.LoadScene(ch, []chars.Character{ca}, msgs)
}

// LoadScene displays a conversation and everyone in it.
//
// A cast of one is what LoadChat passes, and it has to behave identically: the
// prompt, the rows and the grouping of a two-hander cannot change because the
// code that draws them learned to count.
func (c *ChatView) LoadScene(ch store.Chat, cast []chars.Character, msgs []store.Message) {
	c.Clear()
	var ca chars.Character
	if len(cast) > 0 {
		ca = cast[0]
	}
	c.chat, c.char, c.cast = ch, ca, cast
	// Anyone who has spoken but is no longer in the cast. Read once here rather
	// than resolved per row, and only for a scene that has a cast at all.
	if ch.ID != 0 && len(cast) > 1 {
		if spoken, err := c.store.SpeakersIn(ch.ID); err == nil {
			c.spoken = spoken
		} else {
			log.Printf("astral: reading who has spoken in chat %d: %v", ch.ID, err)
		}
	}
	c.recap, c.recapUpto = ch.Summary, ch.SummaryUpto
	c.loadLore(c.loreHost())
	c.mode = Roleplay
	switch ch.Kind {
	case store.KindDesigner, store.KindAssistant, store.KindStyleDesigner, store.KindWorldDesigner:
		c.mode = Plain
	}
	c.refreshModelChip()
	c.refreshPlaceholder()
	c.refreshActions()
	for _, member := range cast {
		c.warnIfCardTooLarge(member)
	}
	c.warnIfCastTooLarge()

	// Only the tail is built; the rest waits behind the button below.
	if len(msgs) > renderWindow {
		c.older = msgs[:len(msgs)-renderWindow]
		msgs = msgs[len(msgs)-renderWindow:]
	}
	c.refreshEarlierButton()
	for _, m := range msgs {
		row := c.appendRowAs(m.CharacterID, m.Role, m.Content, m.Thinking, m.ID, m.CreatedAt)
		if m.TokPerSec > 0 && c.cfg.ShowStats {
			row.SetMeta(ollama.Stats{Tokens: m.EvalCount, TokPerSec: m.TokPerSec}.Summary())
		}
	}
	c.scrollToBottom()
	c.settled = true
	c.focusComposer()
}

// FocusComposer puts the cursor in the input.
func (c *ChatView) FocusComposer() { c.focusComposer() }

func (c *ChatView) focusComposer() {
	if c.composer != nil {
		c.composer.GrabFocus()
	}
}

// speakerFor returns the display name, initial and tint for a turn.
//
// A speaker id names one of the cast; zero means the scene's own character, which
// is every turn in a scene with one.
func (c *ChatView) speakerFor(role string, speaker int64) (string, string, int) {
	if role != ollama.RoleUser {
		if ca, ok := c.castByID(speaker); ok {
			return ca.Name, ca.Initial(), ca.Accent
		}
	}
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
	case store.KindWorldDesigner:
		return "World designer", "✦", 2
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
	return c.appendRowAs(0, role, text, thinking, id, when)
}

// appendRowAs adds a turn spoken by a particular member of the cast. A speaker
// of zero means whoever this scene's single character is, which is every message
// in a scene that has one.
func (c *ChatView) appendRowAs(speaker int64, role, text, thinking string, id int64, when time.Time) *MessageRow {
	// A run of messages from one speaker reads as a single turn in the
	// conversation, so only the first carries a name and an avatar. Two
	// characters in a row are two speakers, however, so the run is broken by a
	// change of either.
	grouped := c.lastRole() == role && c.lastSpeaker() == speaker
	row := c.newRow(speaker, role, text, thinking, id, when, grouped)
	c.markArriving(row)
	c.column.Append(row.Widget())
	c.rows = append(c.rows, row)
	return row
}

// newRow builds a row without placing it, so older batches can be inserted
// above the transcript rather than appended to it.
func (c *ChatView) newRow(speaker int64, role, text, thinking string, id int64, when time.Time, grouped bool) *MessageRow {
	name, initial, accent := c.speakerFor(role, speaker)
	face := c.char
	if ca, ok := c.castByID(speaker); ok {
		face = ca
	}
	opts := MessageOpts{
		Role:        role,
		DisplayName: name,
		Initial:     initial,
		Accent:      accent,
		Mode:        c.proseFor(role),
		Grouped:     grouped,
		When:        when,
	}
	// A fresh widget per row: a GtkPicture cannot be parented twice, so the
	// image is loaded again rather than shared. It is cheap, and GTK caches
	// the decoded texture behind the filename.
	if role != ollama.RoleUser && !grouped && face.AvatarPath != "" {
		opts.Avatar = NewCharacterAvatar(face, avatarSize)
	}
	row := NewMessageRow(opts)
	row.ID = id
	row.Speaker = speaker
	row.SetMarkdown(text)
	row.SetThinking(thinking)
	c.attachActions(row)
	return row
}

// proseFor picks how a message body is rendered, which depends on who wrote it.
//
// A roleplay transcript infers narration from everything outside quotation
// marks, because a model forgets its asterisks often enough that waiting for it
// was measurably hopeless. That reasoning does not reach your own messages: you
// put the asterisks where you meant them, so your text is rendered as you wrote
// it and the renderer keeps its opinions to itself.
func (c *ChatView) proseFor(role string) Prose {
	if c.mode == Roleplay && role == ollama.RoleUser {
		return RoleplayAsWritten
	}
	return c.mode
}

// attachActions adds the per-message hover buttons.
func (c *ChatView) attachActions(row *MessageRow) {
	row.AddAction(IconCopy, "Copy this message", func() {
		if d := gdk.DisplayGetDefault(); d != nil {
			d.Clipboard().SetText(row.Text())
		}
	})
	row.AddAction(IconEdit, "Edit this message", func() {
		c.editRow(row)
	})
	if row.Role == ollama.RoleAssistant {
		row.AddAction(IconHistory, "Continue this reply", func() {
			c.continueReply(row)
		})
	}
	if row.Role == ollama.RoleAssistant {
		row.AddAction(IconRegenerate, "Write this reply again", func() {
			c.regenerate(row)
		})
	}
	row.AddAction(IconTrash, "Delete this message", func() {
		c.deleteRow(row)
	})
}

// editRow lets a turn be corrected in place, and saves it.
func (c *ChatView) editRow(row *MessageRow) {
	if c.busy {
		// Editing the transcript underneath a reply being written to it would
		// change the prompt that reply was built from.
		c.fail("Wait for the reply to finish before editing it.")
		return
	}
	EditMessage(c.widget, row, func(text string) bool {
		if row.ID != 0 && c.store != nil {
			if err := c.store.SetMessageContent(row.ID, text); err != nil {
				c.fail("Could not save the edit: " + err.Error())
				return false
			}
		}
		row.SetMarkdown(text)
		c.notifyChanged()
		return true
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
		row := c.newRow(m.CharacterID, m.Role, m.Content, m.Thinking, m.ID, m.CreatedAt, grouped)
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

// lastSpeaker is which member of the cast wrote the last row, so a change of
// character breaks the run of grouped bubbles as a change of role does.
func (c *ChatView) lastSpeaker() int64 {
	if len(c.rows) == 0 {
		return 0
	}
	return c.rows[len(c.rows)-1].Speaker
}

// loreHost is the character whose world supplies this scene's setting. For a
// cast that is the first member that belongs to one: a scene drawn from two
// worlds has to happen in one of them.
func (c *ChatView) loreHost() chars.Character {
	for _, ca := range c.cast {
		if ca.WorldID != 0 {
			return ca
		}
	}
	return c.char
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
	case store.KindWorldDesigner:
		label, tip = "Create world", "Turn this conversation into a world and its lorebook"
		fire = func() {
			if c.OnBuildWorld != nil {
				c.OnBuildWorld()
			}
		}
	default:
		// A roleplay scene gets the direction chip instead. It lives above the
		// composer rather than behind a menu because a direction is something
		// you set while reading a reply and change a turn later, and anything
		// that takes two clicks to reach does not get used that way.
		if c.char.Name != "" {
			c.actionBar.Append(c.directionChip())
			// Only where there is somebody to add. A scene in a world plays the
			// place and whoever you meet there, so its cast is written as the
			// scene goes and not chosen from a list.
			if c.chat.WorldID == 0 || c.isGroup() {
				c.actionBar.Append(c.castChip())
			}
			c.actionBar.SetVisible(true)
			return
		}
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

// directionChip is the scene-direction control: what it currently says, or an
// invitation to say something.
func (c *ChatView) directionChip() *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("chat-action-chip")
	if note := strings.TrimSpace(c.chat.Note); note != "" {
		// Shown expanded. The model is sent the raw form, because a direction
		// written once should keep working if the scene changes character,
		// but a chip reading "{{char}} is close to admitting..." is a chip
		// asking to be read twice.
		userName := c.cfg.PersonaName
		if userName == "" {
			userName = chars.DefaultPersonaName
		}
		shown := chars.Substitute(note, c.char.Name, userName)
		btn.SetLabel("Direction: " + Snippet(shown, 60))
		btn.AddCSSClass("direction-set")
		btn.SetTooltipText(shown + "\n\nClick to change or clear it.")
	} else {
		btn.SetLabel("Set a direction")
		btn.SetTooltipText("Tell the scene where to go next, without saying it out loud in the story")
	}
	btn.ConnectClicked(func() {
		if c.OnEditDirection != nil {
			c.OnEditDirection()
		}
	})
	return btn
}

// Note returns this scene's direction.
func (c *ChatView) Note() string { return c.chat.Note }

// SetNote stores a new direction for this scene and refreshes the chip.
//
// It takes effect on the next turn, not this one: the reply being read was
// written before the direction existed. Nothing is written for a chat that
// does not exist yet, which is the case before the first message.
func (c *ChatView) SetNote(note string) error {
	note = strings.TrimSpace(note)
	c.chat.Note = note
	c.refreshActions()
	if c.chat.ID == 0 {
		return nil
	}
	return c.store.SetChatNote(c.chat.ID, note)
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

// DevActionCounts builds the hover buttons on the first and last rows and
// reports how many each got, so the lazy path is exercised in a run that
// cannot hover.
func (c *ChatView) DevActionCounts() (first, last int) {
	if len(c.rows) == 0 {
		return 0, 0
	}
	return c.rows[0].DevActionCount(), c.rows[len(c.rows)-1].DevActionCount()
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

// Lore exposes the loaded lorebook, for the auto-update pass.
func (c *ChatView) Lore() (world.World, []world.Entry) { return c.world, c.lore }

// SetLore replaces the loaded lorebook after the model has updated it.
func (c *ChatView) SetLore(entries []world.Entry) { c.lore = entries }

// bgWork is the single lane the background model calls share.
//
// Only one runs at a time, and the user's next turn cancels whatever is in it.
// Cancelling is safe because both passes are idempotent: each records how far
// it got only on success, so an interrupted one simply does the same work after
// the next reply. Waiting, by contrast, is not free — it is the user watching
// a cursor while the model finishes a summary for them.
type bgWork struct {
	running bool
	cancel  context.CancelFunc
	// what names the pass in flight, for the log when it is cut short.
	what string
}

// take claims the lane, returning a context for the work and whether it was
// free. A caller that is refused does nothing: it will be offered the lane
// again after the next reply.
func (b *bgWork) take(what string, timeout time.Duration) (context.Context, bool) {
	if b.running {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	b.running, b.cancel, b.what = true, cancel, what
	return ctx, true
}

// done releases the lane.
func (b *bgWork) done() {
	if b.cancel != nil {
		b.cancel()
	}
	b.running, b.cancel, b.what = false, nil, ""
}

// yield gives the lane up for something the user is waiting on.
func (b *bgWork) yield() {
	if !b.running {
		return
	}
	log.Printf("astral: interrupting the %s pass, the user sent a message", b.what)
	b.done()
}

// warnIfCardTooLarge says so when a character's own card does not fit the
// context window.
//
// This is worth interrupting for because the symptom is otherwise invisible
// and looks like the model misbehaving: the server drops the front of an
// oversized prompt, which is the framing and the writing style, and the scene
// quietly stops following rules nobody can see it was given.
func (c *ChatView) warnIfCardTooLarge(ca chars.Character) {
	if ca.Name == "" {
		return
	}
	b := c.budget(ca, c.persona())
	if !b.Overflows {
		return
	}
	fixed := len(chars.BuildSystem(ca, c.persona()))
	c.fail(fmt.Sprintf(
		"%s's description is too long for the context size. It needs about %d tokens on its own, "+
			"and the window is %d. Shorten the card, or raise the context size in Settings.",
		ca.Name, fixed/4, c.cfg.NumCtx))
}
