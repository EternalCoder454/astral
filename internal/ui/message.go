package ui

import (
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"astral/internal/ollama"
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
	// avatar are omitted and the bubbles read as one run — the thing that
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
	widget  *gtk.Box
	bubble  *gtk.Box
	body    *gtk.Label
	meta    *gtk.Label
	name    *gtk.Label
	actions *gtk.Box

	dots      *TypingDots
	streaming bool

	thinkBox    *gtk.Box
	thinkToggle *gtk.ToggleButton
	thinkLabel  *gtk.Label
	thinkRevea  *gtk.Revealer

	// ID is the database row this displays, 0 while a reply is still streaming
	// and has not been written yet.
	ID   int64
	Role string

	// raw is the full text as received, kept because the label holds *markup*
	// and there is no faithful way back from that to the original.
	raw      string
	thinking string
	mode     Prose
	when     time.Time
}

// NewMessageRow builds a turn.
func NewMessageRow(o MessageOpts) *MessageRow {
	m := &MessageRow{Role: o.Role, mode: o.Mode, when: o.When}
	fromUser := o.Role == ollama.RoleUser

	m.widget = gtk.NewBox(gtk.OrientationHorizontal, 8)
	m.widget.AddCSSClass("message-row")
	if fromUser {
		m.widget.AddCSSClass("from-user")
		m.widget.SetHAlign(gtk.AlignEnd)
	} else {
		m.widget.SetHAlign(gtk.AlignStart)
	}
	// halign only sizes a widget to its natural width while nothing is asking
	// to expand. hexpand propagates up from any descendant that sets it, and a
	// single one anywhere in the row makes GTK treat halign as Fill — which is
	// how a bubble whose natural width was correctly capped at ~430px ended up
	// allocated 800. Denying expansion explicitly at every level of the row is
	// what makes the cap take effect.
	m.widget.SetHExpand(false)
	if o.Grouped {
		m.widget.AddCSSClass("grouped")
	}

	// The avatar is pinned to the top so it stays level with the first line of
	// a long message rather than drifting to the middle of it.
	avatar := m.buildAvatar(o, fromUser)

	col := gtk.NewBox(gtk.OrientationVertical, 2)
	col.SetHExpand(false)
	if fromUser {
		col.SetHAlign(gtk.AlignEnd)
	} else {
		col.SetHAlign(gtk.AlignStart)
	}

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
	m.body.SetXAlign(0)
	m.body.SetYAlign(0)
	m.body.SetSelectable(true)
	m.body.SetCanFocus(false) // selectable, but it must not steal focus from the composer
	m.body.SetHExpand(false)
	m.body.AddCSSClass("message-body")
	m.bubble.Append(m.body)

	// The bubble goes inside a clamp, and this is the part that actually
	// controls its width. max-width-chars below bounds the label's *natural*
	// width, but a wrapping GtkLabel also answers "how wide to fit this height"
	// during allocation, and that answer grows with the amount of text — which
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
	m.actions = gtk.NewBox(gtk.OrientationHorizontal, 2)
	if fromUser {
		foot.Append(m.actions)
		foot.Append(m.meta)
	} else {
		foot.Append(m.meta)
		foot.Append(m.actions)
	}
	col.Append(foot)

	if fromUser {
		m.widget.Append(col)
		m.widget.Append(avatar)
	} else {
		m.widget.Append(avatar)
		m.widget.Append(col)
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
	m.thinkToggle.SetLabel("Show reasoning")
	m.thinkToggle.SetHAlign(gtk.AlignStart)
	m.thinkToggle.AddCSSClass("thinking-toggle")

	m.thinkLabel = gtk.NewLabel("")
	m.thinkLabel.SetWrap(true)
	m.thinkLabel.SetWrapMode(pango.WrapWordChar)
	m.thinkLabel.SetMaxWidthChars(bubbleChars)
	m.thinkLabel.SetXAlign(0)
	m.thinkLabel.SetSelectable(true)
	m.thinkLabel.SetCanFocus(false)
	m.thinkLabel.AddCSSClass("thinking-body")

	m.thinkRevea = gtk.NewRevealer()
	m.thinkRevea.SetChild(m.thinkLabel)
	m.thinkRevea.SetTransitionType(gtk.RevealerTransitionTypeSlideDown)
	m.thinkToggle.ConnectToggled(func() {
		on := m.thinkToggle.Active()
		m.thinkRevea.SetRevealChild(on)
		if on {
			m.thinkToggle.SetLabel("Hide reasoning")
		} else {
			m.thinkToggle.SetLabel("Show reasoning")
		}
	})
	m.thinkBox.Append(m.thinkToggle)
	m.thinkBox.Append(m.thinkRevea)
	// Prepended: reasoning precedes the reply it produced.
	m.bubble.Prepend(m.thinkBox)
}

// Widget returns the row's root widget.
func (m *MessageRow) Widget() gtk.Widgetter { return m.widget }

// AddAction adds a hover button to the row's footer.
func (m *MessageRow) AddAction(iconName, tooltip string, onClick func()) {
	b := gtk.NewButtonFromIconName(iconName)
	b.SetTooltipText(tooltip)
	b.AddCSSClass("message-action")
	b.ConnectClicked(onClick)
	m.actions.Append(b)
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
	if maxWidth > 0 {
		m.body.SetSizeRequest(maxWidth, -1)
	}
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
	m.body.SetSizeRequest(-1, -1)
	m.body.SetVisible(true)
	m.Render()
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
	if maxWidth > 0 {
		m.body.SetSizeRequest(maxWidth, -1)
	}
	m.body.SetText(m.raw)
	m.body.SetVisible(true)
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
		m.body.SetVisible(true)
	}
	m.raw += s
	m.body.SetText(m.raw)
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
		return
	}
	m.body.SetMarkup(Markup(m.raw, m.mode))
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
		parts = append(parts, m.when.Format("15:04"))
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	text := strings.Join(parts, " · ")
	m.meta.SetText(text)
	m.meta.SetVisible(text != "")
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
