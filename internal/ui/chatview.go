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
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
	"astral/internal/world"
)

// flushInterval is how often streamed tokens are drained into the label.
//
// The obvious implementation, hop to the main thread once per token, costs
// an idle source and a full relayout per token, and since each update rewrites
// the whole label it is quadratic in the length of the reply. A local model
// emitting 60 tokens a second turns that into real jank halfway through a long
// roleplay response. Buffering into a builder and flushing on a timer makes
// the cost proportional to elapsed time instead of to token count, and 50ms is
// fast enough that it still reads as typing.
const flushInterval = 50

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
//
// It was 120, and a row's first layout costs about 4ms, nearly all of it
// Pango shaping the text again for each width GTK asks about. GtkBox lays out
// every row, on screen or not, so opening a scene held the window for 650ms,
// and at 40 still for 160ms, measured on a library of 150 scenes. A window
// shows three to five replies, so 16 fills it with room to scroll, and the
// rest arrive as you scroll towards them (see watchForEarlier), so nobody has
// to find a button to read back.
const renderWindow = 16

// transcriptHeight is about how tall the transcript will be, before it is
// shown. A chat is loaded while another page is still on screen, and a page
// the stack is not showing has no height of its own, so it is read from
// the nearest thing holding the chat view that has one, less the composer and
// its chips.
func (c *ChatView) transcriptHeight() int {
	if h := c.scroll.Height(); h > 0 && c.widget.Mapped() {
		return h
	}
	for p := c.widget.Parent(); p != nil; p = gtk.BaseWidget(p).Parent() {
		if h := gtk.BaseWidget(p).Height(); h > 0 {
			return max(h-220, 0)
		}
	}
	return 0
}

// firstBuild is how many of the newest messages are built before a chat is
// first drawn: enough to fill the transcript it opens in, at an ordinary
// reply's height, and no more, since every row laid out before that frame is
// time you wait for it. The rest of renderWindow follows straight after, from
// watchForEarlier, above the part you are looking at. Eight rows instead of
// sixteen had a chat on screen in 42ms instead of 59ms, measured over 150
// chats; a tall window gets more, so its first frame is not half empty.
func firstBuild(height int) int {
	if height <= 0 {
		return renderWindow // not laid out yet: build as many as ever
	}
	const typicalRow = 110 // pixels, a reply of a few lines with its footer
	return min(renderWindow, max(8, height/typicalRow+2))
}

// earlierBatch is how many older messages are built at a time as you scroll
// back. Small, because each batch is one frame's work: 16 held the window for
// about 95ms while scrolling, and a few at a time, built again for as long as
// you are near the top, keeps each frame short.
const earlierBatch = 6

// earlierAhead is how far from the top of the transcript, in pixels, the next
// older batch starts being built: far enough that it is usually there before
// you reach it.
const earlierAhead = 1200

// keepBuilt is how many rows may be built before, once you are back at the
// newest message, the ones scrolled past are let go again. See trimBehind.
const keepBuilt = 3 * renderWindow

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
	actionBar *adw.WrapBox
	// lengthAct and writeFirstAct are the Replies chip's actions, holding the
	// scene's choices as their state so its menu marks them, and repliesMenu
	// is that menu. See lengthChip.
	lengthAct, writeFirstAct *gio.SimpleAction
	repliesMenu              *gio.Menu
	sendBtn                  *gtk.Button
	// usage is the ring beside the send button, and its popover: how full
	// the model's memory is. See usage.go.
	usage       *usageView
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
	// glideFrom is where the glide playing now set out from, glideLast where
	// it last put the view, and glideT0 how far through it was when it set
	// out (later than the start when it began as a snap); see glide.
	glideFrom, glideLast, glideT0 float64
	// settled says the transcript on screen is the one that was stored, so a
	// row added from here is new and should arrive rather than appear.
	settled bool

	// attachPath is an image queued for the next message, and attachBtn is
	// the control that queues it. Only a design chat offers this: a vision
	// model reading a reference picture is how a description gets written
	// from something you have rather than something you can describe.
	attachPath string
	attachBtn  *gtk.Button
	// canAttach mirrors the attach button's visibility, because pasting and
	// dropping have to answer the same question the button does and a hidden
	// widget is a poor place to keep the answer.
	canAttach bool
	// The drop indicator: a veil over the whole chat while a file is held over
	// it, saying what will happen to it or why nothing will.
	dropRevealer *gtk.Revealer
	dropTitle    *gtk.Label
	dropHint     *gtk.Label
	root         *gtk.Overlay
	attachChip   *gtk.Box
	// files is text files waiting to go with the next message, and
	// fileChips shows them; see attachfiles.go.
	// youProfile is the persona this chat is played as, when youSet; see
	// chatpersona.go. personaBtn shows it.
	youProfile chars.Profile
	youSet     bool
	personaBtn *gtk.Button
	files      []attachedText
	fileChips  *gtk.Box
	attachName *gtk.Label
	// lastImage is the most recent image sent in this chat, offered as the
	// character's portrait when the card is built.
	lastImage string
	// picture is an attachment waiting to go out with the turn that is being
	// assembled.
	picture pendingPicture
	// swipeBase is the versions of a reply being written again, kept until
	// the new one is stored beside them, or put back if it never arrives.
	swipeBase []store.Version

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
	// once, two large requests against one model, on top of whatever the user
	// did next. Ollama serves them in turn, so the visible effect was the next
	// reply waiting behind a recap the user never asked for and cannot see.
	bg bgWork

	// older holds the part of a long transcript that has not been built as
	// widgets yet, newest last. See renderWindow.
	older      []store.Message
	earlierBtn *gtk.Button
	// loadingEarlier is set while a batch scrolled into is being built.
	loadingEarlier bool
	// trimQueued is set while dropping the rows scrolled past is waiting to
	// run, and trimBlocked is the transcript as it was when it could not be
	// trimmed, so it is not tried again until a row is built or taken away.
	// See trimBehind.
	trimQueued  bool
	trimBlocked trimMark

	pendMu   sync.Mutex
	pendText strings.Builder
	// pendDiscard says the text already shown for this turn was a preamble to
	// a search, and the row should be cleared before anything else is added.
	pendDiscard bool
	// pendStatus is what a search is doing right now, for the footer of the
	// row the answer will go in.
	pendStatus string
	pendThink  strings.Builder
	flushID    coreglib.SourceHandle

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
	// OnBuildPersona turns a Persona Designer chat into a persona.
	OnBuildPersona func()
	// PersonaFor looks up one of your personas, and OnPickPersona asks the
	// app to choose who you are in this chat; see chatpersona.go.
	PersonaFor    func(id int64) (chars.Profile, bool)
	OnPickPersona func()
	// OnSavePrompt is the same for the Prompt Optimizer.
	OnSavePrompt func()
	// OnAttachImage asks the app to choose an image. The app calls
	// AttachImage with the result.
	OnAttachImage func()
	// OnSaveToKnowledge keeps a reply in the knowledge base.
	OnSaveToKnowledge func(text, chatTitle string, chatID int64)
	// OnImageFile is an image dropped on the chat as a file on disk, and
	// OnImageBytes is one pasted or dropped as pixels with no file behind it.
	// Both end the same way, with the app calling AttachImage.
	OnImageFile  func(path string)
	OnImageBytes func(data []byte)
	// OnReplyDone is told when a reply has been written and stored, with the
	// chat's title and the reply, so the window can say so when nobody is
	// looking at it.
	OnReplyDone func(title, text string)
	// OnEditDirection is the direction chip being clicked. The dialog lives in
	// the app layer, like the other editors.
	OnEditDirection func()
	// OnEditCast is the cast chip being clicked, for changing who is in a scene.
	OnEditCast func()
	// OnEditMemory opens a scene's memory: its record and its pins.
	OnEditMemory func()
	// OnBranch makes a new chat from this one up to a message, and opens it.
	OnBranch func(chatID, messageID int64)
	// OnNotice shows that something worked.
	OnNotice func(string)

	// turn steers the next request built, and only that one; see withTurn.
	turn scene.Turn
	// drafting is Write for Me at work, and draftCancel stops it.
	drafting    bool
	draftCancel context.CancelFunc
	draftBtn    *gtk.Button
	ideasBtn    *gtk.MenuButton

	// cast is every character in this scene. One member, or none, is an
	// ordinary conversation and behaves exactly as it did before there were
	// groups. See chatview_cast.go.
	cast []chars.Character
	// spoken is everyone who has said something in this scene, which is not the
	// same list as the cast once somebody has been written out of it. The cast
	// is who can speak next; this is who a stored line can belong to.
	spoken []chars.Character
	// searchNotes is what the turn being streamed looked up on the web, shown
	// above the reply. Cleared at the start of every turn.
	searchNotes string
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
	c.widget.InsertActionGroup("chat", c.chatActions())

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
	c.watchForEarlier()
	c.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	c.scroll.SetVExpand(true)
	c.widget.Append(c.scroll)

	c.widget.Append(c.buildComposer())
	// Both of these need the composer, which is what a key ends up in.
	typingGoesToComposer(c.scroll, c.composer)
	c.pasteImageIntoComposer()

	c.root = c.buildDropOverlay(c.widget)
	c.installImageDrop(c.root)
	return c
}

// Widget returns the panel's root widget.
func (c *ChatView) Widget() gtk.Widgetter { return c.root }

// SetConfig updates the sampling and display settings used for the next turn.
func (c *ChatView) SetConfig(cfg store.Config) {
	c.cfg = cfg
	c.refreshModelChip()
	if c.chat.PersonaID == 0 {
		// Played as whoever is in use, which may just have changed.
		c.loadPersona()
	}
	c.refreshPersonaChip()
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

	c.fileChips = gtk.NewBox(gtk.OrientationHorizontal, 6)
	c.fileChips.SetHAlign(gtk.AlignCenter)
	c.fileChips.SetVisible(false)
	wrap.Append(c.fileChips)

	// A wrapping box: five chips do not fit a narrow window in one line, and
	// each one squeezed to fit read "Add Someo…". A second line costs less.
	c.actionBar = adw.NewWrapBox()
	c.actionBar.SetChildSpacing(6)
	c.actionBar.SetLineSpacing(6)
	c.actionBar.SetAlign(0.5)
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

	c.draftBtn = gtk.NewButtonFromIconName(IconDraft)
	c.draftBtn.AddCSSClass("composer-model")
	c.draftBtn.SetVisible(false)
	c.draftBtn.ConnectClicked(c.WriteForMe)
	tools.Append(c.draftBtn)

	c.ideasBtn = c.ideasButton()
	c.ideasBtn.SetVisible(false)
	tools.Append(c.ideasBtn)

	c.attachBtn = gtk.NewButtonFromIconName(IconFolder)
	c.attachBtn.AddCSSClass("composer-model")
	c.attachBtn.SetTooltipText("Attach a file or a picture")
	c.attachBtn.SetVisible(false)
	c.attachBtn.ConnectClicked(func() {
		if c.OnAttachImage != nil {
			c.OnAttachImage()
		}
	})
	tools.Append(c.attachBtn)

	c.personaBtn = gtk.NewButton()
	c.personaBtn.AddCSSClass("composer-model")
	c.personaBtn.SetVisible(false)
	c.personaBtn.ConnectClicked(func() {
		if c.OnPickPersona != nil {
			c.OnPickPersona()
		}
	})
	tools.Append(c.personaBtn)

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

	tools.Append(c.buildUsageButton())

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
		// Escape stops a reply, the way it stops everything else that is
		// running. It is the key a person reaches for when a reply goes wrong.
		if keyval == gdk.KEY_Escape && c.busy {
			c.Stop()
			return true
		}
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
		c.sendBtn.SetSensitive(!c.drafting && (c.busy || strings.TrimSpace(c.composerText()) != ""))
		c.placeholder.SetVisible(c.composerText() == "")
		if c.drafting {
			return
		}
		c.refreshDraftButton()
		// The first keystroke after a pause gets the model loaded and the
		// scene read, so both happen while the message is written rather
		// than after it is sent. See scene.WarmForTyping for when it declines.
		if !c.busy && c.composerText() != "" {
			c.warmForTyping()
		}
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
		text = "Describe who you want"
	case c.chat.Kind == store.KindStyleDesigner:
		text = "Describe how you want the writing to read"
	case c.chat.Kind == store.KindPromptOptimizer && strings.TrimSpace(c.chat.Note) == "":
		text = "Paste the prompt, and say what it is for"
	case c.chat.Kind == store.KindPromptOptimizer:
		text = "Say what you want changed, or just say go"
	case c.chat.Kind == store.KindAssistant:
		text = "Ask anything"
	case c.chat.Kind == store.KindNovel:
		text = "Say what the story is, or what happens next"
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
		c.modelBtn.SetChild(chipLabel("Choose a Model", 16))
		c.modelBtn.SetTooltipText("Choose the model for this chat")
		return
	}
	// The full tag is a path with a registry org in front of it, and at
	// composer size that is a wall of text sitting where a small control
	// should be. The short form is what distinguishes one of your models from
	// another; the whole thing stays a hover away.
	c.modelBtn.SetChild(chipLabel(shortModel(m), 30))
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
	// A reply being written again when the chat is left will land nowhere, so
	// the one it was replacing goes back into the chat it came from.
	if base := c.swipeBase; base != nil {
		c.swipeBase = nil
		if _, err := c.storeVersions(c.chat.ID, base); err != nil {
			c.fail("Could not put the earlier reply back: " + err.Error())
		}
	}
	// Bumping the generation is what makes an in-flight reply land nowhere:
	// see the staleness guard in finishStream.
	c.gen++
	for _, r := range c.rows {
		c.column.Remove(r.Widget())
		r.Release()
	}
	c.rows = nil
	c.trimBlocked = trimMark{}
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
	if ch.ID == 0 {
		// As a stored chat starts, so Scene Memory shows the same for a
		// scene before its first message as after it.
		ch.SettingAuto = true
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
	case store.KindDesigner, store.KindAssistant, store.KindStyleDesigner, store.KindWorldDesigner,
		store.KindPromptOptimizer, store.KindPersonaDesigner:
		c.mode = Plain
	}
	c.refreshModelChip()
	c.loadPersona()
	c.refreshPlaceholder()
	c.refreshActions()
	for _, member := range cast {
		c.warnIfCardTooLarge(member)
	}
	c.warnIfCastTooLarge()

	// Only the tail is built; the rest waits behind the button below.
	if first := firstBuild(c.transcriptHeight()); len(msgs) > first {
		c.older = msgs[:len(msgs)-first]
		msgs = msgs[len(msgs)-first:]
	}
	c.refreshEarlierButton()
	for _, m := range msgs {
		row := c.appendRowAs(m.CharacterID, m.Role, m.Content, m.Thinking, m.ID, m.CreatedAt)
		row.Versions, row.Version = m.Versions, m.Version
		if m.Pinned {
			row.SetPinned(true)
		}
		if m.Hidden {
			row.SetHidden(true)
		}
		if m.TokPerSec > 0 && c.cfg.ShowStats {
			row.SetMeta(ollama.Stats{Tokens: m.EvalCount, TokPerSec: m.TokPerSec}.Summary())
		}
	}
	c.refreshPagers()
	c.scrollToBottom()
	c.settled = true
	c.focusComposer()
	// Measured after the chat is on screen, not before: measuring builds the
	// next request, and a chat is opened to be read.
	if c.usage != nil {
		c.usage.btn.SetVisible(false)
		id := c.chat.ID
		coreglib.TimeoutAdd(400, func() bool {
			if c.chat.ID == id {
				c.refreshUsage()
			}
			return false
		})
	}
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
	// Before the character, because a designer chat revising somebody has that
	// character on it and the designer is still the one talking.
	if role != ollama.RoleUser {
		switch c.chat.Kind {
		case store.KindDesigner:
			return "Character Designer", "✦", 1
		case store.KindStyleDesigner:
			return "Style Designer", "✦", 3
		case store.KindWorldDesigner:
			return "World Designer", "✦", 2
		case store.KindPromptOptimizer:
			return "Prompt Optimizer", "✦", 4
		case store.KindPersonaDesigner:
			return "Persona Designer", "✦", 2
		}
	}
	if role == ollama.RoleUser {
		name := c.youName()
		return name, firstLetter(name), 0
	}
	if name := c.char.Name; name != "" {
		return name, c.char.Initial(), c.char.Accent
	}
	// The designers are answered above, before the character, so what is left
	// here is a novel, told by its narrator, or a plain conversation.
	if c.chat.Kind == store.KindNovel {
		return "Narrator", "✦", 0
	}
	return "Assistant", "✦", 0
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
	// The arrows belong to the last reply only, so the one that was last
	// loses them.
	if n := len(c.rows); n > 0 {
		c.rows[n-1].SetPager(false, nil, nil)
	}
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
	if role == ollama.RoleUser && !grouped {
		if pic := c.youAvatar(); pic != nil {
			opts.Avatar = pic
		}
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
	// Your own turn, written better by the model, in a scene where it knows
	// how you write.
	if row.Role == ollama.RoleUser && c.canDraft() {
		row.AddAction(IconRegenerate, "Rewrite your message better, and have your last one answered again", func() {
			c.rewriteMine(row)
		})
	}
	if row.Role == ollama.RoleAssistant {
		row.AddAction(IconContinue, "Continue this reply", func() {
			c.continueReply(row)
		})
	}
	if row.Role == ollama.RoleAssistant {
		row.AddAction(IconRegenerate, "Write this reply again", func() {
			c.regenerate(row)
		})
	}
	// Writing it again toward something, in the conversations where a note on
	// a reply means anything: a scene, and a general chat.
	if row.Role == ollama.RoleAssistant && (c.mode == Roleplay || c.chat.Kind == store.KindAssistant) {
		row.AddAction(IconDraft, "Rewrite with a note on what you want from it", func() {
			c.rewriteWithNote(row)
		})
	}
	// Pinning is for a conversation that keeps a recap, which is where
	// something can otherwise be forgotten.
	if c.compactable() {
		row.AddAction(IconPin, "Pin this message so it is never forgotten", func() {
			c.togglePin(row)
		})
	}
	// The rest behind More: used too rarely for a button each, and a row of
	// ten icons under every reply is harder to read than a menu.
	more := []RowMenuItem{{
		Label:   func() string { return "Branch from Here" },
		OnClick: func() { c.branchFrom(row) },
	}, {
		Label: func() string {
			if row.Hidden {
				return "Show to the Model"
			}
			return "Hide from the Model"
		},
		OnClick: func() { c.toggleHidden(row) },
	}}
	// Keeping an answer, in the conversations that draw on what is kept. A
	// scene's replies are fiction, and saving them as knowledge would put a
	// character's opinions in front of the next real question.
	if row.Role == ollama.RoleAssistant && scene.UsesKnowledge(c.chat.Kind) && c.OnSaveToKnowledge != nil {
		more = append(more, RowMenuItem{
			Label:   func() string { return "Save to Knowledge" },
			OnClick: func() { c.OnSaveToKnowledge(row.Text(), c.chat.Title, c.chat.ID) },
		})
	}
	row.AddMenu(more)
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
		if row.Version < len(row.Versions) {
			row.Versions[row.Version].Content = text
		}
		c.notifyChanged()
		return true
	})
}

// refreshPagers puts the version arrows on the last reply, when it has
// versions to flip between, and takes them off everything else.
//
// Only the last: every turn after a reply was written in answer to it, so
// flipping one further up would leave the rest of the scene answering
// something that is no longer there. Not in a group scene, where one turn is
// several replies. And not while a reply is being written.
func (c *ChatView) refreshPagers() {
	for i, r := range c.rows {
		show := i == len(c.rows)-1 && r.Role == ollama.RoleAssistant && !c.busy && !c.isGroup()
		r.SetPager(show, func() { c.flipVersion(r, -1) }, func() { c.flipVersion(r, +1) })
	}
}

// flipVersion shows the version of a reply before or after the one on screen.
// Past the last, it writes another.
func (c *ChatView) flipVersion(row *MessageRow, step int) {
	if c.busy || row.ID == 0 {
		return
	}
	at := row.Version + step
	if at >= len(row.Versions) {
		c.regenerate(row)
		return
	}
	if at < 0 {
		return
	}
	v, err := c.store.SetMessageVersion(row.ID, at)
	if err != nil {
		c.fail("Could not show that version: " + err.Error())
		return
	}
	row.Version = at
	row.SetMarkdown(v.Content)
	row.SetThinking(v.Thinking)
	c.refreshPagers()
	c.notifyChanged()
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
// maximum means a long reply, which is most of them, never reflows at all,
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
	c.earlierBtn.SetLabel(fmt.Sprintf("Show %d Earlier Messages", min(len(c.older), earlierBatch)))
}

// loadEarlier builds the next batch of older messages above what is already
// on screen.
func (c *ChatView) loadEarlier() {
	n := min(len(c.older), earlierBatch)
	batch := c.older[len(c.older)-n:]
	c.older = c.older[:len(c.older)-n]

	// Built in reverse and prepended, so each ends up above the last. Grouping
	// is decided within the batch: the row below a batch is already on screen
	// and its own grouping was settled when it was built.
	rows := make([]*MessageRow, 0, len(batch))
	for i, m := range batch {
		// The same rule as appendRowAs: two characters in a row are two
		// speakers, each with a name.
		grouped := i > 0 && batch[i-1].Role == m.Role && batch[i-1].CharacterID == m.CharacterID
		row := c.newRow(m.CharacterID, m.Role, m.Content, m.Thinking, m.ID, m.CreatedAt, grouped)
		row.Versions, row.Version = m.Versions, m.Version
		if m.Pinned {
			row.SetPinned(true)
		}
		if m.Hidden {
			row.SetHidden(true)
		}
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

// watchForEarlier builds the next older batch when the transcript is scrolled
// near its top, and keeps the message you were reading where it was: the rows
// arrive above it, so the view moves down by exactly their height.
func (c *ChatView) watchForEarlier() {
	adj := c.scroll.VAdjustment()
	load := func() {
		if c.loadingEarlier || len(c.older) == 0 || adj.Value() > earlierAhead {
			return
		}
		c.loadingEarlier = true
		coreglib.IdleAdd(func() bool {
			if len(c.older) == 0 {
				c.loadingEarlier = false
				return false
			}
			oldUpper, oldValue := adj.Upper(), adj.Value()
			var h coreglib.SignalHandle
			done := false
			finish := func() {
				if !done {
					done = true
					adj.HandlerDisconnect(h)
					c.loadingEarlier = false
				}
			}
			h = adj.ConnectChanged(func() {
				if grew := adj.Upper() - oldUpper; grew > 0 && !done {
					adj.SetValue(oldValue + grew)
					finish()
				}
			})
			// However the layout goes, the next batch is never blocked for
			// good by one whose height never arrived.
			coreglib.TimeoutAdd(1500, func() bool { finish(); return false })
			c.loadEarlier()
			return false
		})
	}
	adj.ConnectValueChanged(load)
	adj.ConnectValueChanged(c.maybeTrim)
	// A tail too short to scroll gives no scrolling to wait for, in a tall
	// window or a scene of short lines, so the next batch is built straight
	// away, until the window is full or there is nothing older.
	adj.ConnectChanged(func() {
		if adj.PageSize() > 0 && adj.Upper() <= adj.PageSize() {
			load()
		}
	})
}

// maybeTrim lets go of the rows scrolled past, once you are back at the
// newest message with many more built than a chat opens with.
func (c *ChatView) maybeTrim() {
	if c.trimQueued || len(c.rows) <= keepBuilt || c.trimBlocked == c.mark() || !c.trimmable() {
		return
	}
	// Later, not inside the scroll: this can be called while the transcript
	// is being laid out, and rows cannot be taken away in the middle of that.
	c.trimQueued = true
	coreglib.IdleAdd(func() bool {
		c.trimQueued = false
		c.trimBehind()
		return false
	})
}

// trimmable reports whether the rows scrolled past can go now: at the very
// bottom, with nothing being written or built. A message being rewritten
// counts, because its row is written to when the rewrite comes back.
func (c *ChatView) trimmable() bool {
	if c.live != nil || c.busy || c.drafting || c.loadingEarlier || c.chat.ID == 0 {
		return false
	}
	adj := c.scroll.VAdjustment()
	return adj.PageSize() > 0 && adj.Value() >= adj.Upper()-adj.PageSize()-2
}

// trimBehind puts the rows scrolled past back behind the scroll, the way the
// chat opened, to be built again only if you scroll up to them.
//
// Every row scrolled back through stayed built, so a long read back through
// a scene left hundreds of rows on the window for as long as the chat was
// open. Each is laid out again whenever the window changes width, and each
// holds its share of memory. They are read back from the database rather than
// from the rows, so what is built again is what is stored, pins and edits
// included.
//
// What stays is measured in pixels, not rows: at least a window's height and
// the distance at which older rows are built again, and some to spare (a
// row's height leaves out the gap under it, so a little more), so a trim
// never takes a row that is on screen, and never leaves so little that
// scrolling builds the same rows straight back. Never fewer than a chat
// opens with.
//
// A few at a time, a frame apart: 144 rows taken away at once held the
// window for about 100ms just as you arrived at the bottom.
func (c *ChatView) trimBehind() {
	if len(c.rows) <= keepBuilt || !c.trimmable() {
		return
	}
	heights := make([]int, len(c.rows))
	for i, r := range c.rows {
		heights[i] = gtk.BaseWidget(r.Widget()).Height()
	}
	need := int(c.scroll.VAdjustment().PageSize()) + earlierAhead + trimSpare
	cut := len(c.rows) - keepCount(heights, c.column.Spacing(), need, renderWindow)
	if cut <= 0 {
		return
	}
	block := func() { c.trimBlocked = c.mark() }
	for _, r := range c.rows[:cut] {
		if r.ID == 0 {
			block() // not stored, so it could not be built again
			return
		}
	}
	// Read once for the whole trim, which stops if a reply starts, a
	// message is rewritten, or rows are built or cleared, and the next trim
	// reads again. A pin changed on a row being trimmed, in the moment
	// between two steps, would be built again as it was.
	msgs, err := c.store.Messages(c.chat.ID)
	if err != nil {
		return // perhaps busy for a moment: tried again on the next scroll
	}
	var step func()
	step = func() {
		if !c.trimmable() || cut <= 0 {
			return
		}
		n := min(cut, trimStep)
		if n >= len(c.rows) {
			return
		}
		older, ok := olderThan(msgs, c.rows[n].ID)
		if !ok {
			block()
			return
		}
		for _, r := range c.rows[:n] {
			c.column.Remove(r.Widget())
			r.Release()
		}
		c.rows = append([]*MessageRow(nil), c.rows[n:]...)
		c.older = older
		c.refreshEarlierButton()
		if cut -= n; cut > 0 {
			c.trimQueued = true
			first := c.rows[0]
			coreglib.IdleAdd(func() bool {
				c.trimQueued = false
				if len(c.rows) > 0 && c.rows[0] == first { // nothing built or cleared since
					step()
				}
				return false
			})
		}
	}
	step()
}

// trimMark is the transcript as trimBehind last saw it fail: its first row
// and how many there were. Building a row or taking one away changes it.
type trimMark struct {
	first *MessageRow
	n     int
}

func (c *ChatView) mark() trimMark {
	if len(c.rows) == 0 {
		return trimMark{}
	}
	return trimMark{c.rows[0], len(c.rows)}
}

// trimStep is how many rows trimBehind takes away in one frame, and
// trimSpare how many pixels it keeps beyond what it has to.
const (
	trimStep  = 24
	trimSpare = 600
)

// keepCount is how many of the newest rows, whose heights are given oldest
// first, cover need pixels from the bottom: never fewer than least, and
// never more than there are.
func keepCount(heights []int, spacing, need, least int) int {
	sum, n := 0, 0
	for i := len(heights) - 1; i >= 0 && sum < need; i-- {
		sum += heights[i] + spacing
		n++
	}
	return min(len(heights), max(n, least))
}

// olderThan is the messages before the one with id first, from a chat's
// messages in order; false when first is not among them.
func olderThan(msgs []store.Message, first int64) ([]store.Message, bool) {
	for i, m := range msgs {
		if m.ID == first {
			return msgs[:i], true
		}
	}
	return nil, false
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
// settings. An in-flight reply keeps the client it started with, cancelling
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
	if c.lengthAct != nil {
		c.lengthAct.SetState(glib.NewVariantString(c.chat.ReplyLength))
		c.writeFirstAct.SetState(glib.NewVariantInt64(int64(c.chat.WriteFirst)))
	}
	if c.actionBar == nil {
		return
	}
	c.refreshDraftButton()
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
		label, tip = "Create Character", "Turn this conversation into a character you can play with"
		if c.char.Name != "" {
			label = "Save Character"
			tip = "Write " + c.char.Name + " again from this conversation, keeping their scenes"
		}
		fire = func() {
			if c.OnBuildCharacter != nil {
				c.OnBuildCharacter()
			}
		}
	case store.KindStyleDesigner:
		label, tip = "Create Style", "Turn this conversation into a writing style"
		if name := strings.TrimSpace(c.chat.Note); name != "" {
			label = "Save Style"
			tip = "Write " + name + " again from this conversation"
		}
		fire = func() {
			if c.OnBuildStyle != nil {
				c.OnBuildStyle()
			}
		}
	case store.KindPromptOptimizer:
		label, tip = "Save Prompt", "Use the last reply's prompt from now on"
		if strings.TrimSpace(c.chat.Note) == "" {
			label, tip = "Copy Prompt", "Copy the prompt from the last reply"
		}
		fire = func() {
			if c.OnSavePrompt != nil {
				c.OnSavePrompt()
			}
		}
	case store.KindPersonaDesigner:
		label, tip = "Create Persona", "Turn this conversation into one of your personas"
		fire = func() {
			if c.OnBuildPersona != nil {
				c.OnBuildPersona()
			}
		}
	case store.KindWorldDesigner:
		label, tip = "Create World", "Turn this conversation into a world and its lorebook"
		if c.chat.WorldID != 0 {
			label = "Save World"
			tip = "Write this world again from this conversation, keeping its lorebook"
		}
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
			c.actionBar.Append(c.castChip())
			if c.isGroup() {
				c.actionBar.Append(c.turnChip())
			}
			c.actionBar.Append(c.lengthChip())
			c.actionBar.Append(c.memoryChip())
			c.actionBar.SetVisible(true)
			c.refreshDraftButton()
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
		userName := c.youName()
		shown := chars.Substitute(note, c.char.Name, userName)
		btn.SetChild(chipLabel("Direction: "+Snippet(shown, 60), 48))
		btn.AddCSSClass("direction-set")
		btn.SetTooltipText(shown + "\n\nClick to change or clear it.")
	} else {
		btn.SetChild(chipLabel("Set a Direction", 18))
		btn.SetTooltipText("Tell the scene where to go next")
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

// DevScroll is where the transcript is scrolled to, for the dev harness.
func (c *ChatView) DevScroll() (value, upper, page float64) {
	adj := c.scroll.VAdjustment()
	return adj.Value(), adj.Upper(), adj.PageSize()
}

// DevColumnWidth is the width the transcript column is laid out at.
func (c *ChatView) DevColumnWidth() int { return c.column.Width() }

// DevLoadEarlier builds the next older batch, so the dev harness can exercise
// the path a click takes without a click.
func (c *ChatView) DevLoadEarlier() { c.loadEarlier() }

// DevSend types a message and sends it, for the dev harness, which has no way
// to type.
func (c *ChatView) DevSend(text string) {
	c.setComposerText(text)
	c.Send()
}

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
	c.canAttach = can
	if c.attachBtn != nil {
		c.attachBtn.SetVisible(can || c.acceptsFiles())
	}
	if !can {
		c.AttachImage("")
	}
	if !c.acceptsFiles() {
		c.clearFiles()
	}
}

// CanAttachImages reports whether a picture would be taken, for the app's
// file chooser.
func (c *ChatView) CanAttachImages() bool { return c.canAttach }

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
// the next reply. Waiting, by contrast, is not free, it is the user watching
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
		"%s's card needs about %d tokens, too many for a context of %d, so shorten it or raise the context size.",
		ca.Name, fixed/4, c.cfg.NumCtx))
}

// DevRequestModes lists the widget tree of the last message row with each
// widget's size request mode, for the dev harness.
func (c *ChatView) DevRequestModes() []string {
	if len(c.rows) == 0 {
		return nil
	}
	var out []string
	var walk func(w *gtk.Widget, depth int)
	walk = func(w *gtk.Widget, depth int) {
		out = append(out, fmt.Sprintf("%s%s %v %v", strings.Repeat("  ", depth), w.CSSName(), w.CSSClasses(), w.RequestMode()))
		for ch := w.FirstChild(); ch != nil; ch = gtk.BaseWidget(ch).NextSibling() {
			walk(gtk.BaseWidget(ch), depth+1)
		}
	}
	walk(gtk.BaseWidget(c.rows[len(c.rows)-1].Widget()), 0)
	return out
}
