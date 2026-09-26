package ui

import (
	"math"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Movement in the transcript is here to answer two questions the eye asks:
// what just changed, and did my press register. Nothing else moves.
//
// The cost of getting this wrong is specific. A transcript animates while
// someone is reading it, so anything long enough to notice is long enough to
// be in the way, and anything that moves text someone is mid-sentence on is
// worse than no animation at all. So arrival is a fade with eight pixels of
// travel, presses are a two percent shrink, and the scroll glide is the only
// thing that moves the reader's view. All three are short.

// arriving is the class that plays the fade-up in style.css. It is added and
// never removed: a CSS animation runs once from the moment the class lands,
// and taking it off again would cost a timeout per message for nothing.
const arriving = "arriving"

// glideDuration is how long the transcript takes to scroll itself to a new
// message, in milliseconds.
const glideDuration = 220

// glideMin is the distance, in pixels, below which a glide is not worth
// playing. Under this the movement reads as a flicker rather than as travel,
// and it is the common case when a chat is nearly at the bottom already.
const glideMin = 24

// markArriving gives a row the arrival animation, if the transcript it is
// joining is one someone is already looking at.
//
// The condition is the whole point. LoadChat appends every stored message
// through the same path, and forty rows fading up at once does not read as
// forty new messages, it reads as a window that has not finished drawing.
func (c *ChatView) markArriving(row *MessageRow) {
	if row == nil || !c.settled {
		return
	}
	if w := gtk.BaseWidget(row.Widget()); w != nil {
		w.AddCSSClass(arriving)
	}
}

// scrollToBottom moves the transcript to the end at once.
//
// Used where the content is still arriving: streaming calls it on every flush,
// and a chat being opened wants to be at the bottom before it is looked at, not
// on its way there.
func (c *ChatView) scrollToBottom() { c.scheduleScroll(false) }

// glideToBottom moves the transcript to the end with travel.
//
// Used where the reason to scroll is something the reader did: they sent a
// message, or a greeting arrived. Streaming does not use it, because streaming
// scrolls twenty times a second and an animation that is re-aimed on every
// flush never arrives anywhere.
func (c *ChatView) glideToBottom() { c.scheduleScroll(true) }

// scheduleScroll queues one scroll to the end of the transcript.
//
// Deferred to idle: the adjustment's upper bound is only correct once GTK has
// laid out the new row. Coalesced: streaming asks twenty times a second and
// gotk4 keeps every idle closure for the life of the process.
//
// A snap overrides a glide that has not run yet, and stops one that is playing.
// The only reason to ask for a snap is that more content has arrived, which is
// newer information than the glide was aimed with.
func (c *ChatView) scheduleScroll(travel bool) {
	if !travel {
		c.scrollTravel = false
		c.stopGlide()
	} else if !c.scrollPending {
		c.scrollTravel = true
	}
	if c.scrollPending {
		return
	}
	c.scrollPending = true
	coreglib.IdleAdd(func() bool {
		c.scrollPending = false
		glide := c.scrollTravel
		c.scrollTravel = false

		adj := c.scroll.VAdjustment()
		to := adj.Upper() - adj.PageSize()
		if !glide || math.Abs(to-adj.Value()) < glideMin {
			adj.SetValue(to)
			return false
		}
		c.glide(adj.Value(), to)
		return false
	})
}

// glide animates the scroll position. One animation object is kept and re-aimed
// rather than built per scroll: an adw.Animation is a GObject with a callback
// attached, and building one per message would accumulate them for the life of
// the window.
func (c *ChatView) glide(from, to float64) {
	if c.scrollAnim == nil {
		target := adw.NewCallbackAnimationTarget(func(v float64) {
			c.scroll.VAdjustment().SetValue(v)
		})
		c.scrollAnim = adw.NewTimedAnimation(c.scroll, from, to, glideDuration, target)
		c.scrollAnim.SetEasing(adw.EaseOutCubic)
	}
	c.scrollAnim.SetValueFrom(from)
	c.scrollAnim.SetValueTo(to)
	c.scrollAnim.Play()
}

// stopGlide abandons a glide in progress, leaving the view where it had got to.
//
// Called by the snapping path, because the two write the same adjustment: a
// glide still playing when streaming starts would drag the view back up on
// every frame while the tokens push it down.
func (c *ChatView) stopGlide() {
	if c.scrollAnim == nil {
		return
	}
	if c.scrollAnim.State() == adw.AnimationPlaying {
		c.scrollAnim.Pause()
	}
}
