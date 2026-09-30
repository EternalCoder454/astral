package ui

import (
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/store"
)

// bubbleChars bounds a message's natural width, in characters, and is what
// decides bubble width on a large display; the clamp below only bites once
// space is tight.
//
// A wrapping GtkLabel's natural width is its text on one unbroken line, so
// without a cap it stretches its parent or gets clipped. 80 is wider than the
// ~65 typography would pick, because at 60 a conversation looks lost on a 4K
// screen.
const bubbleChars = 80

// bodyMinChars is the narrowest a message's text wraps to, in characters,
// once it has a line well past that: inside the narrowest window, and wide
// enough that working out that width is cheap. See setFloor.
const bodyMinChars = 20

// bubbleMaxWidth is the bubble clamp's maximum, in pixels: a backstop for
// windows wide enough that height-for-width would run away, since bubbleChars
// usually gets there first.
//
// It is not the width a bubble ends up; GTK's negotiation lands wider. Change
// it by measuring (ASTRAL_DEV_VIEW=measure prints every bubble's real width).
const bubbleMaxWidth = 780

// avatarSize is both the avatar's size and the width of the spacer that stands
// in for it on a grouped message, so consecutive bubbles stay aligned.
const avatarSize = 28

// MessageOpts describes a turn to be rendered.
type MessageOpts struct {
	Role        string
	DisplayName string
	Initial     string
	Accent      int
	Mode        Prose
	// Grouped means the turn above is from the same speaker, so the name and
	// avatar are omitted and the bubbles read as one run, the thing that
	// makes a long exchange look like a conversation rather than a list.
	Grouped bool
	When    time.Time
	// Avatar, when set, is used in place of the letter tile.
	Avatar gtk.Widgetter
}

// MessageRow is one turn in the transcript, laid out as a chat bubble: the
// character on the left, you on the right.
//
// The body is a GtkLabel rather than a TextView because it has to render Pango
// markup (italic action, weighted speech), wrap to the bubble, and be
// selectable, and a label does all three without a text buffer's machinery.
type MessageRow struct {
	widget  *gtk.Grid
	bubble  *gtk.Box
	body    *gtk.Label
	meta    *gtk.Label
	name    *gtk.Label
	actions *gtk.FlowBox
	// pending holds the hover buttons until the row is first hovered; armed
	// says a controller is watching for that, and built that it has happened.
	pending []rowAction
	armed   bool
	built   bool
	// buttons are the built actions by icon, so one whose meaning flips,
	// pinning, can say what it will do now.
	buttons  map[string]*gtk.Button
	pinMark  *gtk.Image
	hideMark *gtk.Label
	// signals are the handlers the row has connected on its own widgets, kept
	// so Release can take them off again. See Release for why that matters.
	signals []rowSignal

	dots      *TypingDots
	streaming bool
	// While a reply streams, its finished paragraphs are frozen into labels
	// of their own in stream, and only the paragraph still being written, in
	// tail, is set again as tokens arrive. frozen is how much of raw the
	// frozen labels hold, and streamWidth the width they are pinned to.
	//
	// One label holding the whole reply was laid out again, all of it, twenty
	// times a second: measured on an 8,600-character reply, frames went from
	// 18ms to over 100ms as it grew, and the window fell to eight frames a
	// second by the end.
	stream      *gtk.Box
	tail        *gtk.Label
	frozen      int
	streamWidth int

	thinkBox    *gtk.Box
	thinkToggle *gtk.ToggleButton
	thinkLabel  *gtk.Label
	thinkRevea  *gtk.Revealer

	// ID is the database row this displays, 0 while a reply is still streaming
	// and has not been written yet.
	ID int64
	// Speaker is which member of the cast said it, in a scene with more than
	// one character. Zero everywhere else.
	Speaker int64
	Role    string
	// Versions are the replies written for this turn when it has been written
	// more than once, and Version is which is showing. See store.Version.
	Versions []store.Version
	Version  int
	// Pinned mirrors the stored flag; SetPinned changes both it and the mark.
	Pinned bool
	// Hidden mirrors the stored flag; see SetHidden.
	Hidden bool

	foot      *gtk.Box
	pager     *gtk.Box
	pageLabel *gtk.Label
	pagePrev  *gtk.Button
	pageNext  *gtk.Button
	onPrev    func()
	onNext    func()

	// raw is the full text as received, kept because the label holds *markup*
	// and there is no faithful way back from that to the original.
	raw      string
	thinking string
	mode     Prose
	when     time.Time
}

// DevRows counts rows built and rows Go has since freed, when set, for the dev
// harness's check that a chat left behind is let go of.
var DevRows *struct{ Built, Freed atomic.Int64 }

// NewMessageRow builds a turn.
func NewMessageRow(o MessageOpts) *MessageRow {
	m := &MessageRow{Role: o.Role, mode: o.Mode, when: o.When}
	if d := DevRows; d != nil {
		d.Built.Add(1)
		runtime.SetFinalizer(m, func(*MessageRow) { d.Freed.Add(1) })
	}
	fromUser := o.Role == ollama.RoleUser

	// A grid rather than a horizontal box, and filling the row with a spacer
	// taking what the message does not, rather than aligned to its side: both
	// for the time a chat takes to open, which they halved.
	//
	// A box shares out its width by asking each child how wide it would be at
	// the row's height, and GTK places an aligned widget whose height depends
	// on its width by asking the same. For the column of wrapping text beside
	// the avatar each ask is a search over widths, with every label in it laid
	// out again at each step. A grid settles the widths first, without a
	// height, and a column that fills its cell is not asked.
	m.widget = gtk.NewGrid()
	m.widget.AddCSSClass("message-row")
	if fromUser {
		m.widget.AddCSSClass("from-user")
	}
	// The spacer is what expands. Said here that the row does not, so that
	// stops at the row rather than spreading up through the transcript.
	m.widget.SetHExpand(false)
	if o.Grouped {
		m.widget.AddCSSClass("grouped")
	}

	// The avatar is pinned to the top so it stays level with the first line of
	// a long message rather than drifting to the middle of it.
	avatar := m.buildAvatar(o, fromUser)
	// The gap between the avatar and the message, kept on the avatar: the
	// grid's own spacing would put one beside the spacer too, and take it
	// from the message.
	if fromUser {
		gtk.BaseWidget(avatar).SetMarginStart(8)
	} else {
		gtk.BaseWidget(avatar).SetMarginEnd(8)
	}

	col := gtk.NewBox(gtk.OrientationVertical, 2)
	col.SetHExpand(false)

	// The speaker's name, on their first message in a run. Not shown for your
	// own turns: a messaging app does not label your side "You", and the
	// alignment already says whose it is.
	if !fromUser && !o.Grouped {
		m.name = gtk.NewLabel(o.DisplayName)
		m.name.SetXAlign(0)
		m.name.AddCSSClass("message-name")
		col.Append(m.name)
	}

	m.bubble = gtk.NewBox(gtk.OrientationVertical, 4)
	m.bubble.SetHExpand(false)
	m.bubble.AddCSSClass("bubble")
	if fromUser {
		m.bubble.AddCSSClass("bubble-user")
		m.bubble.SetHAlign(gtk.AlignEnd)
	} else {
		m.bubble.AddCSSClass("bubble-char")
		m.bubble.SetHAlign(gtk.AlignStart)
	}
	// The reasoning block and the typing indicator are built on demand.
	// Constructed eagerly they are five widgets on every row in the
	// transcript, and the overwhelming majority of rows never show either: a
	// loaded chat has no indicator, and most models emit no reasoning at all.

	m.body = gtk.NewLabel("")
	m.body.SetWrap(true)
	m.body.SetWrapMode(pango.WrapWordChar) // a long URL must not widen the bubble
	m.body.SetMaxWidthChars(bubbleChars)   // see the constant: this is what makes wrapping work
	// And a floor under it once there is text: see setFloor.
	m.body.SetXAlign(0)
	m.body.SetYAlign(0)
	// Selectable, and allowed to take focus, because in GTK4 a label holds a
	// selection only while it can be focused. Setting can-focus false here read
	// as "selectable but do not steal focus from the composer" and actually
	// meant the selection could never be made: the label highlighted nothing and
	// there was no way to copy part of a reply.
	//
	// What it costs is a stop in the Tab chain per message. The composer is
	// focused on opening a chat and after every send, so the cursor still starts
	// and returns where it should.
	m.body.SetSelectable(true)
	m.body.SetHExpand(false)
	m.body.AddCSSClass("message-body")
	m.bubble.Append(m.body)

	// The bubble goes inside a clamp, and this is the part that actually
	// controls its width. max-width-chars below bounds the label's *natural*
	// width, but a wrapping GtkLabel also answers "how wide to fit this height"
	// during allocation, and that answer grows with the amount of text, which
	// is why bubbles were coming out 800px wide with a natural of 430, and why
	// neither halign nor hexpand=false made any difference. A clamp is the one
	// thing that overrides it.
	bc := adw.NewClamp()
	bc.SetMaximumSize(bubbleMaxWidth)
	bc.SetTighteningThreshold(bubbleMaxWidth)
	bc.SetChild(m.bubble)
	bc.SetHExpand(false)
	col.Append(bc)

	// Footer: time and throughput, with the hover actions beside them.
	foot := gtk.NewBox(gtk.OrientationHorizontal, 4)
	foot.SetHExpand(false)
	if fromUser {
		foot.SetHAlign(gtk.AlignEnd)
	} else {
		foot.SetHAlign(gtk.AlignStart)
	}
	m.meta = gtk.NewLabel("")
	m.meta.AddCSSClass("message-meta")
	m.meta.SetVisible(false)
	// A flow box rather than a row, so the buttons wrap onto a second line in
	// a narrow window. In a row their combined width was the narrowest any
	// message could be, and so the narrowest the window could be.
	m.actions = gtk.NewFlowBox()
	m.actions.AddCSSClass("message-actions")
	m.actions.SetSelectionMode(gtk.SelectionNone)
	m.actions.SetHomogeneous(false)
	m.actions.SetMinChildrenPerLine(1)
	m.actions.SetMaxChildrenPerLine(16)
	m.actions.SetColumnSpacing(2)
	m.actions.SetRowSpacing(2)
	m.actions.SetVAlign(gtk.AlignCenter)
	m.foot = foot
	if fromUser {
		foot.Append(m.actions)
		foot.Append(m.meta)
	} else {
		foot.Append(m.meta)
		foot.Append(m.actions)
	}
	col.Append(foot)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	if fromUser {
		m.widget.Attach(spacer, 0, 0, 1, 1)
		m.widget.Attach(col, 1, 0, 1, 1)
		m.widget.Attach(avatar, 2, 0, 1, 1)
	} else {
		m.widget.Attach(avatar, 0, 0, 1, 1)
		m.widget.Attach(col, 1, 0, 1, 1)
		m.widget.Attach(spacer, 2, 0, 1, 1)
	}
	m.SetMeta("")
	return m
}

// buildAvatar returns the speaker's avatar, or an invisible spacer of the same
// width when the message is grouped with the one above it.
func (m *MessageRow) buildAvatar(o MessageOpts, fromUser bool) gtk.Widgetter {
	if o.Grouped {
		spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
		spacer.SetSizeRequest(avatarSize, 1)
		return spacer
	}
	if o.Avatar != nil {
		gtk.BaseWidget(o.Avatar).SetVAlign(gtk.AlignStart)
		return o.Avatar
	}
	var avatar *gtk.Label
	if fromUser {
		avatar = NewUserAvatar(o.Initial, avatarSize)
	} else {
		avatar = NewAvatar(o.Initial, o.Accent, avatarSize)
	}
	avatar.SetVAlign(gtk.AlignStart)
	return avatar
}

// ensureThinking creates the collapsed reasoning block on first use.
func (m *MessageRow) ensureThinking() {
	if m.thinkBox != nil {
		return
	}
	m.thinkBox = gtk.NewBox(gtk.OrientationVertical, 0)
	m.thinkToggle = gtk.NewToggleButton()
	m.thinkToggle.SetLabel("Show Reasoning")
	m.thinkToggle.SetHAlign(gtk.AlignStart)
	m.thinkToggle.AddCSSClass("thinking-toggle")

	m.thinkLabel = gtk.NewLabel("")
	m.thinkLabel.SetWrap(true)
	m.thinkLabel.SetWrapMode(pango.WrapWordChar)
	m.thinkLabel.SetMaxWidthChars(bubbleChars)
	m.thinkLabel.SetXAlign(0)
	m.thinkLabel.SetSelectable(true) // and focusable: see typingGoesToComposer

	m.thinkLabel.AddCSSClass("thinking-body")

	m.thinkRevea = gtk.NewRevealer()
	m.thinkRevea.SetChild(m.thinkLabel)
	m.thinkRevea.SetTransitionType(gtk.RevealerTransitionTypeSlideDown)
	m.track(m.thinkToggle, m.thinkToggle.ConnectToggled(func() {
		on := m.thinkToggle.Active()
		m.thinkRevea.SetRevealChild(on)
		if on {
			m.thinkToggle.SetLabel("Hide Reasoning")
		} else {
			m.thinkToggle.SetLabel("Show Reasoning")
		}
	}))
	m.thinkBox.Append(m.thinkToggle)
	m.thinkBox.Append(m.thinkRevea)
	// Prepended: reasoning precedes the reply it produced.
	m.bubble.Prepend(m.thinkBox)
}

// Widget returns the row's root widget.
func (m *MessageRow) Widget() gtk.Widgetter { return m.widget }

// rowSignal is one handler a row connected, and what it is connected on.
type rowSignal struct {
	obj *coreglib.Object
	h   coreglib.SignalHandle
}

// track records a handler connected on one of the row's own widgets.
func (m *MessageRow) track(obj coreglib.Objector, h coreglib.SignalHandle) {
	m.signals = append(m.signals, rowSignal{coreglib.BaseObject(obj), h})
}

// Release takes off every handler the row connected, once it has left the
// transcript for good.
//
// Without this a row is never freed. A handler's Go function is held by the
// widget it is connected on, and GTK keeps that widget while its parent
// lives; the function holds the row, and the row holds its root widget, which
// Go then never lets go of, so the parent lives too. Measured on a library of
// 150 chats, opening each of them once kept 400MB that never came back, and
// every pass through them kept 400MB more.
func (m *MessageRow) Release() {
	for _, s := range m.signals {
		s.obj.HandlerDisconnect(s.h)
	}
	m.signals = nil
	m.pending = nil
	m.onPrev, m.onNext = nil, nil
	m.stopDots()
}

// AddAction registers a hover button for the row's footer.
//
// The button is not built here. These are invisible until the pointer is over
// the row, and a transcript holds hundreds of rows with five each: measured on
// six hundred messages, the buttons alone were 23MB of the 73MB the rows cost.
// So the description is kept and the widget is made the first time the row is
// hovered or focused, which is the first moment anyone could use it.
func (m *MessageRow) AddAction(iconName, tooltip string, onClick func()) {
	m.pending = append(m.pending, rowAction{icon: iconName, tooltip: tooltip, onClick: onClick})
	m.armActions()
}

// rowAction is a button that has not been built yet, or with menu set, a
// More button whose menu lists the rest.
type rowAction struct {
	icon    string
	tooltip string
	onClick func()
	menu    []RowMenuItem
}

// RowMenuItem is one entry in a row's More menu. Label is asked each time the
// menu opens, so an entry whose meaning flips, hiding, says what it will do.
type RowMenuItem struct {
	Label   func() string
	OnClick func()
}

// AddMenu adds the row's More button, listing the actions used too rarely to
// earn a button of their own.
func (m *MessageRow) AddMenu(items []RowMenuItem) {
	if len(items) == 0 {
		return
	}
	m.pending = append(m.pending, rowAction{icon: IconMore, tooltip: "More", menu: items})
	m.armActions()
}

// menuButton builds a More button.
func (m *MessageRow) menuButton(items []RowMenuItem) *gtk.MenuButton {
	mb := gtk.NewMenuButton()
	mb.SetIconName(IconMore)
	mb.SetTooltipText("More")
	mb.AddCSSClass("message-action")
	pop := gtk.NewPopover()
	box := gtk.NewBox(gtk.OrientationVertical, 2)
	var buttons []*gtk.Button
	for _, it := range items {
		it := it
		b := gtk.NewButtonWithLabel(it.Label())
		b.AddCSSClass("flat")
		gtk.BaseWidget(b.Child()).SetHAlign(gtk.AlignStart)
		m.track(b, b.ConnectClicked(func() {
			pop.Popdown()
			it.OnClick()
		}))
		box.Append(b)
		buttons = append(buttons, b)
	}
	m.track(pop, pop.ConnectShow(func() {
		for i, it := range items {
			buttons[i].SetLabel(it.Label())
			gtk.BaseWidget(buttons[i].Child()).SetHAlign(gtk.AlignStart)
		}
	}))
	pop.SetChild(box)
	mb.SetPopover(pop)
	return mb
}

// armActions makes sure something is watching for the first hover.
//
// Keyboard focus counts: the buttons are reachable by tabbing into the row, and
// a row that only built them on a mouse hover would be a row that could not be
// tabbed into at all.
func (m *MessageRow) armActions() {
	if m.armed || m.actions == nil {
		return
	}
	m.armed = true
	motion := gtk.NewEventControllerMotion()
	m.track(motion, motion.ConnectEnter(func(x, y float64) { m.buildActions() }))
	m.widget.AddController(motion)

	focus := gtk.NewEventControllerFocus()
	m.track(focus, focus.ConnectEnter(func() { m.buildActions() }))
	m.widget.AddController(focus)
}

// buildActions materialises the buttons, once.
func (m *MessageRow) buildActions() {
	if m.built || len(m.pending) == 0 {
		return
	}
	m.built = true
	m.buttons = make(map[string]*gtk.Button, len(m.pending))
	for _, a := range m.pending {
		if a.menu != nil {
			m.appendAction(m.menuButton(a.menu))
			continue
		}
		b := gtk.NewButtonFromIconName(a.icon)
		b.SetTooltipText(a.tooltip)
		b.AddCSSClass("message-action")
		m.track(b, b.ConnectClicked(a.onClick))
		m.appendAction(b)
		m.buttons[a.icon] = b
	}
	m.pending = nil
}

// appendAction adds a built button to the actions. The flow box wraps each
// in a child of its own, which would otherwise take a stop in the Tab order
// before the button it holds.
func (m *MessageRow) appendAction(w gtk.Widgetter) {
	m.actions.Append(w)
	if p := gtk.BaseWidget(w).Parent(); p != nil {
		gtk.BaseWidget(p).SetFocusable(false)
	}
}

// SetActionTooltip changes what an action says it does, built or not.
func (m *MessageRow) SetActionTooltip(icon, tooltip string) {
	for i := range m.pending {
		if m.pending[i].icon == icon {
			m.pending[i].tooltip = tooltip
		}
	}
	if b, ok := m.buttons[icon]; ok {
		b.SetTooltipText(tooltip)
	}
}

// SetHidden shows whether this message is hidden from the model: set back,
// and saying so under it.
func (m *MessageRow) SetHidden(hidden bool) {
	m.Hidden = hidden
	if hidden {
		m.widget.AddCSSClass("hidden-from-model")
		m.SetActionTooltip(IconHidden, "Show this to the model again")
	} else {
		m.widget.RemoveCSSClass("hidden-from-model")
		m.SetActionTooltip(IconHidden, "Hide this from the model, and keep it here")
	}
	if m.hideMark == nil {
		if !hidden {
			return
		}
		m.hideMark = gtk.NewLabel("Hidden from the model")
		m.hideMark.AddCSSClass("message-meta")
		m.hideMark.AddCSSClass("hide-mark")
		m.foot.Append(m.hideMark)
	}
	m.hideMark.SetVisible(hidden)
}

// SetPinned shows whether this message is pinned, with a mark in its footer
// that stays when the pointer leaves, unlike the actions.
func (m *MessageRow) SetPinned(pinned bool) {
	m.Pinned = pinned
	if pinned {
		m.SetActionTooltip(IconPin, "Unpin this message")
	} else {
		m.SetActionTooltip(IconPin, "Pin this message so it is never forgotten")
	}
	if m.pinMark == nil {
		if !pinned {
			return
		}
		m.pinMark = gtk.NewImageFromIconName(IconPin)
		m.pinMark.AddCSSClass("pin-mark")
		m.pinMark.SetTooltipText("Pinned: always kept in mind, however long the scene grows")
		if m.Role == ollama.RoleUser {
			m.foot.Append(m.pinMark)
		} else {
			m.foot.Prepend(m.pinMark)
		}
	}
	m.pinMark.SetVisible(pinned)
}

// DevActionCount builds this row's hover buttons and says how many there are.
//
// Only the dev harness calls it. The buttons are built on hover, and a
// headless run cannot hover, so this is how that path is checked at all.
func (m *MessageRow) DevActionCount() int {
	m.buildActions()
	n := 0
	for child := m.actions.FirstChild(); child != nil; child = gtk.BaseWidget(child).NextSibling() {
		n++
	}
	return n
}

// BeginStreaming prepares the row to receive a reply: the typing indicator
// starts, and the bubble's width is pinned to maxWidth.
//
// Pinning the width is what stops the bubble thrashing. A wrapping label
// recomputes how wide it wants to be from how much text it holds, so appending
// tokens made the bubble renegotiate its width several times a second and the
// text visibly reflowed and folded in on itself while being written. Fixing
// the width means the reply only ever grows downward, which is what reading
// text being typed should look like.
func (m *MessageRow) BeginStreaming(maxWidth int) {
	m.streaming = true
	m.streamWidth = maxWidth
	if maxWidth > 0 {
		m.body.SetSizeRequest(maxWidth, -1)
	}
	m.fillWhileStreaming()
	if m.dots == nil {
		m.dots = NewTypingDots()
		m.bubble.Append(m.dots)
	}
	m.body.SetVisible(false) // nothing to show yet; the dots stand in
	m.dots.Start()
}

// EndStreaming releases the pinned width and renders the finished text, so the
// bubble shrink-wraps a short reply the way a loaded one does.
func (m *MessageRow) EndStreaming() {
	m.streaming = false
	m.stopDots()
	m.dropStream()
	m.body.SetSizeRequest(-1, -1)
	if m.Role != ollama.RoleUser {
		m.bubble.SetHAlign(gtk.AlignStart)
	}
	m.body.SetVisible(true)
	m.Render()
}

// fillWhileStreaming lets the bubble take its column's width while a reply is
// written into it, rather than keeping to its side.
//
// GTK places a widget aligned to one side by asking how wide it would be at
// the height it is given, and for a column of wrapping labels that is a
// search over widths with every label laid out again at each step. A reply
// grows taller with each flush, so no answer was ever reused: every
// paragraph already written was laid out again, several times, twenty times
// a second. Filling, it is simply given the column's width, which does not
// change, and only the paragraph still being written is laid out.
//
// The width is the same either way while streaming, since the text is pinned
// to it (see BeginStreaming); EndStreaming puts the alignment back so a
// finished short reply shrinks to its text.
func (m *MessageRow) fillWhileStreaming() {
	if m.Role != ollama.RoleUser && m.streamWidth > 0 {
		m.bubble.SetHAlign(gtk.AlignFill)
	}
}

// streamLabel is one paragraph of a reply being written, styled as the body.
func (m *MessageRow) streamLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.SetMaxWidthChars(bubbleChars)
	l.SetXAlign(0)
	l.SetYAlign(0)
	l.SetHExpand(false)
	l.AddCSSClass("message-body")
	if m.streamWidth > 0 {
		l.SetSizeRequest(m.streamWidth, -1)
		// A floor under the wrapping, for the same reason as setFloor. The
		// window asks for its narrowest after every flush, and without one
		// GTK finds this label's by laying the paragraph out at no width at
		// all, a character to a line. Pinned, the label is wider than the
		// floor anyway, so it changes nothing on screen.
		l.SetWidthChars(bodyMinChars)
	}
	return l
}

// showStream puts the text streamed so far on screen: every finished paragraph
// frozen in a label of its own, and the one still being written in the tail.
func (m *MessageRow) showStream() {
	if m.stream == nil {
		// The same gap a blank line leaves inside a label, so the reply does
		// not change shape when it is drawn as one at the end.
		m.stream = gtk.NewBox(gtk.OrientationVertical, 14)
		m.stream.SetHExpand(false)
		m.tail = m.streamLabel("")
		m.stream.Append(m.tail)
		m.bubble.InsertChildAfter(m.stream, m.body)
	}
	m.body.SetVisible(false)
	for {
		rest := m.raw[m.frozen:]
		i := strings.Index(rest, "\n\n")
		if i < 0 {
			break
		}
		if para := strings.TrimSpace(rest[:i]); para != "" {
			l := m.streamLabel(para)
			m.stream.InsertChildAfter(l, prevSibling(m.tail))
		}
		m.frozen += i + 2
	}
	m.tail.SetText(strings.TrimLeft(m.raw[m.frozen:], "\n"))
}

// prevSibling is the widget before w in its parent, or nil when w is first,
// which InsertChildAfter reads as "at the start".
func prevSibling(w *gtk.Label) gtk.Widgetter {
	if p := w.PrevSibling(); p != nil {
		return p
	}
	return nil
}

// dropStream takes the streaming labels away.
func (m *MessageRow) dropStream() {
	if m.stream != nil {
		m.bubble.Remove(m.stream)
		m.stream, m.tail = nil, nil
	}
	m.frozen = 0
}

// AppendText adds to the body during streaming.
// ContinueStreaming prepares a row that already holds text to receive more.
//
// Unlike BeginStreaming there are no dots and the body stays visible: there is
// already something to read, and hiding it behind an indicator would be a step
// backwards. The text goes back to plain while it streams, for the same reason
// it is plain in a new reply: half an asterisk is not markup.
func (m *MessageRow) ContinueStreaming(maxWidth int) {
	m.streaming = true
	m.streamWidth = maxWidth
	if maxWidth > 0 {
		m.body.SetSizeRequest(maxWidth, -1)
	}
	m.fillWhileStreaming()
	m.dropStream()
	m.showStream()
}

func (m *MessageRow) AppendText(s string) {
	if s == "" {
		return
	}
	// The first token replaces the indicator. Doing it here rather than on a
	// timer means the dots are shown for exactly as long as there is nothing
	// else to show.
	if m.streaming && m.raw == "" {
		m.stopDots()
	}
	m.raw += s
	if m.streaming {
		m.showStream()
		return
	}
	m.body.SetText(m.raw)
	m.setFloor()
}

// ClearStreamed takes back the text streamed so far, for a turn whose first
// words turned out to be a preamble to a search rather than the answer.
func (m *MessageRow) ClearStreamed() {
	m.raw = ""
	m.dropStream()
	m.body.SetText("")
	m.setFloor()
	if m.streaming {
		m.showStream()
	}
}

// AppendThinking adds to the reasoning block, revealing it on first use.
func (m *MessageRow) AppendThinking(s string) {
	if s == "" {
		return
	}
	// Reasoning arriving is also a sign of life, so the dots give way to it.
	if m.streaming && m.thinking == "" {
		m.stopDots()
	}
	m.ensureThinking()
	m.thinking += s
	m.thinkLabel.SetText(m.thinking)
}

// Render draws the accumulated text as markup. Called once a turn is complete,
// and when loading a chat from the database.
func (m *MessageRow) Render() {
	if strings.TrimSpace(m.raw) == "" {
		m.body.SetText("")
		m.setFloor()
		return
	}
	// A file sent with a message is folded to its name on screen; the model
	// still reads all of it, from the message as stored.
	m.body.SetMarkup(Markup(chars.HideAttachedFiles(m.raw), m.mode))
	m.setFloor()
}

// setFloor puts a floor under how narrow the text may wrap, for the time it
// takes to lay out. Without one GTK finds a wrapping label's narrowest by
// laying the whole text out at no width at all, broken at every place it can
// be, and did that for every message each time a chat was opened.
//
// Only under text with a line well past it. The floor is also the label's
// narrowest, and GTK sizes it by a wide average character, so a floor under
// "Yes." gave it a bubble twenty characters wide, and one under a line of
// twenty narrow ones widened that too.
func (m *MessageRow) setFloor() {
	floor := -1
	for _, line := range strings.Split(m.body.Text(), "\n") {
		if utf8.RuneCountInString(line) >= 2*bodyMinChars {
			floor = bodyMinChars
			break
		}
	}
	if m.body.WidthChars() != floor {
		m.body.SetWidthChars(floor)
	}
}

// SetMarkdown sets the body and renders it in one step.
func (m *MessageRow) SetMarkdown(s string) {
	m.raw = s
	m.Render()
}

// SetThinking sets the reasoning text wholesale. Nothing is built when there
// is no reasoning, which is the common case.
func (m *MessageRow) SetThinking(s string) {
	if strings.TrimSpace(s) == "" {
		m.thinking = ""
		if m.thinkBox != nil {
			m.thinkBox.SetVisible(false)
		}
		return
	}
	m.thinking = s
	m.ensureThinking()
	m.thinkLabel.SetText(s)
	m.thinkBox.SetVisible(true)
}

// stopDots halts the indicator if one was ever created.
func (m *MessageRow) stopDots() {
	if m.dots != nil {
		m.dots.Stop()
	}
}

// Text returns the raw message text.
func (m *MessageRow) Text() string { return m.raw }

// Thinking returns the raw reasoning text.
func (m *MessageRow) Thinking() string { return m.thinking }

// SetMeta sets the footer caption. The time is always shown when known; extra
// (throughput) is appended after it.
func (m *MessageRow) SetMeta(extra string) {
	parts := make([]string, 0, 2)
	if !m.when.IsZero() {
		parts = append(parts, whenLabel(m.when, time.Now()))
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	text := strings.Join(parts, " · ")
	m.meta.SetText(text)
	m.meta.SetVisible(text != "")
}

// SetPager shows which of a turn's versions is on screen, with arrows to the
// others. The arrow past the last one asks for another, the way writing the
// reply again does, so the two are one gesture rather than two buttons.
// Hidden when show is false, and built only the first time it is shown.
func (m *MessageRow) SetPager(show bool, onPrev, onNext func()) {
	if !show || len(m.Versions) < 2 {
		if m.pager != nil {
			m.pager.SetVisible(false)
		}
		return
	}
	m.onPrev, m.onNext = onPrev, onNext
	if m.pager == nil {
		m.pager = gtk.NewBox(gtk.OrientationHorizontal, 0)
		m.pager.AddCSSClass("version-pager")
		m.pagePrev = gtk.NewButtonWithLabel("‹")
		m.pagePrev.AddCSSClass("flat")
		m.pagePrev.SetTooltipText("The reply before this one")
		m.track(m.pagePrev, m.pagePrev.ConnectClicked(func() {
			if m.onPrev != nil {
				m.onPrev()
			}
		}))
		m.pageLabel = gtk.NewLabel("")
		m.pageLabel.AddCSSClass("message-meta")
		m.pageNext = gtk.NewButtonWithLabel("›")
		m.pageNext.AddCSSClass("flat")
		m.track(m.pageNext, m.pageNext.ConnectClicked(func() {
			if m.onNext != nil {
				m.onNext()
			}
		}))
		m.pager.Append(m.pagePrev)
		m.pager.Append(m.pageLabel)
		m.pager.Append(m.pageNext)
		m.foot.Prepend(m.pager)
	}
	m.pageLabel.SetText(fmt.Sprintf("%d/%d", m.Version+1, len(m.Versions)))
	m.pagePrev.SetSensitive(m.Version > 0)
	if m.Version >= len(m.Versions)-1 {
		m.pageNext.SetTooltipText("Write another")
	} else {
		m.pageNext.SetTooltipText("The reply after this one")
	}
	m.pager.SetVisible(true)
}

// whenLabel is a message's time as its footer shows it: the time alone for
// today, and the date as well for anything older. A scene picked up after a
// week read as though every line was said this morning.
func whenLabel(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return t.Format("15:04")
	case y1 == y2:
		return t.Format("2 Jan, 15:04")
	default:
		return t.Format("2 Jan 2006, 15:04")
	}
}

// BubbleWidth reports the laid-out width of the bubble, for the dev harness's
// wrapping check.
func (m *MessageRow) BubbleWidth() int { return m.bubble.Width() }

// DevLineMetrics reports how tall the body is and how many lines it wrapped
// to, so the real line spacing can be measured rather than assumed. GTK's
// line-height interacts with the font's own metrics, and the rendered result
// is not the number in the stylesheet.
func (m *MessageRow) DevLineMetrics() (height, lines int) {
	h := m.body.Height()
	layout := m.body.Layout()
	if layout == nil {
		return h, 0
	}
	return h, layout.LineCount()
}
