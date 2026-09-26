package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/ollama"
	"astral/internal/scene"
	"astral/internal/store"
)

// A scene with several characters in it is one transcript, not several.
//
// The model returns the whole turn in one reply with the speakers marked in it,
// and this file is what turns that back into rows: a beat becomes its own
// message, with its own name, tint and face, so the transcript reads as a
// conversation between people rather than as a wall of labelled paragraphs.
//
// Everything here is inert when the cast has fewer than two members, which is
// most scenes. That is deliberate rather than defensive: an ordinary scene has
// to keep producing exactly the prompt and exactly the rows it produced before.

// isGroup reports whether this scene has a cast rather than a single character.
func (c *ChatView) isGroup() bool { return len(c.cast) > 1 }

// castNames is the cast's names, for the prompt and for the splitter.
func (c *ChatView) castNames() []string { return chars.CastNames(c.cast) }

// castMember finds a member by name, as the splitter reports it.
func (c *ChatView) castMember(name string) (chars.Character, bool) {
	for _, ca := range c.cast {
		if strings.EqualFold(ca.Name, name) {
			return ca, true
		}
	}
	return chars.Character{}, false
}

// castByID finds whoever a stored message belongs to.
//
// It looks through everyone who has spoken in this scene rather than only the
// current cast, because a character written out of a scene keeps the lines they
// already said, and those lines keep their name and their face.
func (c *ChatView) castByID(id int64) (chars.Character, bool) {
	if id == 0 {
		return chars.Character{}, false
	}
	for _, ca := range c.cast {
		if ca.ID == id {
			return ca, true
		}
	}
	for _, ca := range c.spoken {
		if ca.ID == id {
			return ca, true
		}
	}
	return chars.Character{}, false
}

// nameOf is the name to put back on a stored beat when the transcript is sent to
// the model. A character deleted out from under an old scene has no name left,
// and an unlabelled beat is better than a beat labelled with nothing.
func (c *ChatView) nameOf(id int64) string {
	if ca, ok := c.castByID(id); ok {
		return ca.Name
	}
	return ""
}

// speakerIDFor resolves a beat's name to a character id.
//
// An unnamed beat is the first member of the cast: the model wrote before it
// named anyone, and somebody has to have said it. The first member is the one
// the scene is titled after, which makes it the least surprising answer.
func (c *ChatView) speakerIDFor(name string) int64 {
	if name != "" {
		if ca, ok := c.castMember(name); ok {
			return ca.ID
		}
	}
	if len(c.cast) > 0 {
		return c.cast[0].ID
	}
	return 0
}

// beatRow is the row a streaming beat is going into, plus who it belongs to.
type beatRow struct {
	row  *MessageRow
	who  int64
	text strings.Builder
}

// streamBeats routes a chunk of a group reply into rows, opening a new one every
// time the speaker changes.
func (c *ChatView) streamBeats(text string) {
	for _, b := range c.beats.Next(text) {
		who := c.speakerIDFor(b.Name)
		switch {
		case len(c.liveRows) == 0:
			// The row the typing indicator was shown in, now that there is
			// somebody to attribute it to.
			c.adoptFirstBeatRow(who)
		case c.liveRows[len(c.liveRows)-1].who != who:
			c.finishBeatRow()
			r := c.appendRowAs(who, ollama.RoleAssistant, "", "", 0, time.Now())
			r.BeginStreaming(c.streamWidth())
			c.live = r
			c.liveRows = append(c.liveRows, &beatRow{row: r, who: who})
		}
		br := c.liveRows[len(c.liveRows)-1]
		// The leading blank line between beats belongs to the gap between two
		// rows, not to the top of the second one.
		chunk := b.Text
		if br.text.Len() == 0 {
			chunk = strings.TrimLeft(chunk, " \t\r\n")
			if chunk == "" {
				continue
			}
		}
		br.text.WriteString(chunk)
		br.row.AppendText(chunk)
		br.row.SetMeta("")
	}
}

// adoptFirstBeatRow attributes the row the reply started streaming into.
//
// The row exists before the first beat does, because the typing indicator has to
// appear the moment the request goes out and the speaker is not known until the
// label has arrived. When the label names somebody else the row is rebuilt, which
// is invisible: nothing has been shown in it yet but the dots.
func (c *ChatView) adoptFirstBeatRow(who int64) {
	row := c.live
	if row == nil {
		return
	}
	// A continuation already has a row, and a stored message behind it. Keeping
	// it is not an optimisation: replacing it would leave the saved first half
	// with no row to rewrite, and the turn would be stored twice.
	if row.ID != 0 {
		br := &beatRow{row: row, who: row.Speaker}
		// Seeded with what is already there, so the first chunk of a
		// continuation is not treated as the start of a beat and stripped of the
		// space that joins it to the sentence it is finishing.
		br.text.WriteString(row.Text())
		c.liveRows = append(c.liveRows, br)
		return
	}
	if row.Speaker != who {
		c.removeRow(row)
		row = c.appendRowAs(who, ollama.RoleAssistant, "", "", 0, time.Now())
		row.BeginStreaming(c.streamWidth())
		c.live = row
	}
	c.liveRows = append(c.liveRows, &beatRow{row: row, who: who})
}

// finishBeatRow closes the row a beat was streaming into.
func (c *ChatView) finishBeatRow() {
	if n := len(c.liveRows); n > 0 {
		c.liveRows[n-1].row.EndStreaming()
	}
}

// layOutBeats replaces what was streamed with the finished reply, split by
// speaker.
//
// The stream and the one-pass split agree by construction, so in the ordinary
// case every row already holds what it is given here and this only renders the
// markup. It is done from the finished text anyway because that text is what
// gets stored, and a transcript whose rows disagree with the database is one that
// changes when you reopen it.
func (c *ChatView) layOutBeats(beats []chars.Beat, thinking, meta string) {
	if !c.beatRowsMatch(beats) {
		c.rebuildBeatRows(beats)
	}
	for i, b := range beats {
		br := c.liveRows[i]
		br.row.EndStreaming()
		br.row.SetMarkdown(b.Text)
	}
	if len(c.liveRows) == 0 {
		return
	}
	// The deliberation and the statistics belong to the turn rather than to a
	// beat, so they go on its first and last row instead of on every one.
	if thinking != "" {
		c.liveRows[0].row.SetThinking(thinking)
	}
	if meta != "" {
		c.liveRows[len(c.liveRows)-1].row.SetMeta(meta)
	}
}

// beatRowsMatch reports whether the rows the reply streamed into line up with
// the finished split, speaker for speaker.
func (c *ChatView) beatRowsMatch(beats []chars.Beat) bool {
	if len(c.liveRows) != len(beats) {
		return false
	}
	for i, b := range beats {
		if c.liveRows[i].who != c.speakerIDFor(b.Name) {
			return false
		}
	}
	return true
}

// rebuildBeatRows throws away the streamed rows and lays the turn out again.
//
// It should not run: the streaming splitter and the one-pass splitter are the
// same code reading the same bytes, and a test asserts they agree for any
// chunking. It is here because "should not" is not "cannot", and the failure it
// would otherwise produce is a reply attributed to the wrong faces.
//
// Row ids are carried over by position so a turn that has already been saved is
// rewritten rather than duplicated, and any message left without a row is
// deleted rather than orphaned in the transcript.
func (c *ChatView) rebuildBeatRows(beats []chars.Beat) {
	ids := make([]int64, 0, len(c.liveRows))
	for _, br := range c.liveRows {
		ids = append(ids, br.row.ID)
		c.removeRow(br.row)
	}
	c.liveRows = c.liveRows[:0]
	for i, b := range beats {
		who := c.speakerIDFor(b.Name)
		r := c.appendRowAs(who, ollama.RoleAssistant, b.Text, "", 0, time.Now())
		if i < len(ids) {
			r.ID = ids[i]
		}
		c.liveRows = append(c.liveRows, &beatRow{row: r, who: who})
	}
	for _, id := range ids[min(len(beats), len(ids)):] {
		if id == 0 {
			continue
		}
		if err := c.store.DeleteMessage(id); err != nil {
			c.fail("Could not tidy up the reply: " + err.Error())
		}
	}
}

// saveBeats writes a group turn to the store, one message per beat.
//
// The statistics go on the last one. They describe the whole turn, and putting
// them on every beat would report the same numbers three times for one reply.
func (c *ChatView) saveBeats(stats ollama.Stats) {
	for i, br := range c.liveRows {
		text := strings.TrimSpace(br.row.Text())
		if text == "" {
			continue
		}
		m := store.Message{
			ChatID:      c.chat.ID,
			Role:        ollama.RoleAssistant,
			Content:     text,
			CharacterID: br.who,
		}
		if i == 0 {
			m.Thinking = br.row.Thinking()
		}
		if i == len(c.liveRows)-1 {
			m.EvalCount, m.TokPerSec = stats.Tokens, stats.TokPerSec
		}
		if br.row.ID != 0 {
			if err := c.store.SetMessageContent(br.row.ID, text); err != nil {
				c.fail("Could not save the reply: " + err.Error())
			}
			continue
		}
		id, err := c.store.AddMessage(m)
		if err != nil {
			c.fail("Could not save the reply: " + err.Error())
			return
		}
		br.row.ID = id
	}
}

// clearBeats forgets the rows a group turn was streamed into.
func (c *ChatView) clearBeats() {
	c.liveRows = nil
	c.beats = chars.BeatStream{}
}

// finishGroupTurn lands a finished group reply: split it, lay it out, store it.
//
// It takes over from finishStream at the point where a two-hander would render
// one row, because a group turn is several rows and none of them is the one the
// reply started in.
func (c *ChatView) finishGroupTurn(content, thinking string, stats ollama.Stats, started time.Time, cancelled bool) {
	defer c.clearBeats()

	beats := chars.SplitBeats(content, c.castNames())
	if len(beats) == 0 {
		for _, br := range c.liveRows {
			c.removeRow(br.row)
		}
		c.liveRows = nil
		c.live = nil
		switch {
		case thinking != "":
			c.fail("The model spent its whole reply limit thinking. Turn reasoning off, or raise the reply limit, in Settings.")
		case !cancelled:
			c.fail("The model returned an empty reply.")
		}
		return
	}

	meta := c.turnMeta(&stats, started, cancelled)
	c.layOutBeats(beats, thinking, meta)
	c.saveBeats(stats)
	c.live = nil

	c.notifyChanged()
	if c.atBottom() {
		c.scrollToBottom()
	}
	c.checkModelFits(c.activeModel())
	// Compaction first, and only one of the two can run: a scene that has
	// outgrown its window needs the recap before it needs new lore.
	c.maybeCompact()
	c.maybeLearn()
}

// greetingSpeaker is who opens a scene. In a group that is the first member of
// the cast, whose greeting is the one shown; elsewhere it is nobody in
// particular, because there is only one character it could be.
func (c *ChatView) greetingSpeaker() int64 {
	if c.isGroup() && len(c.cast) > 0 {
		return c.cast[0].ID
	}
	return 0
}

// sceneCast is the cast to assemble a prompt or a recap for: the scene's own
// when it has one, and otherwise the single character, so every scene that
// existed before groups did takes exactly the path it took.
func (c *ChatView) sceneCast() []chars.Character {
	if c.isGroup() {
		return c.cast
	}
	return []chars.Character{c.char}
}

// sceneBudget divides the context window for this scene, whoever is in it.
//
// A group's cards are measured, not estimated. Five descriptions are thousands
// of characters that have to come out of the transcript, and a group planned as
// a two-hander thinks it has room it does not have: compaction waits too long,
// and the server drops the front of the prompt instead, which is the framing.
func (c *ChatView) sceneBudget() chars.Budget {
	// A plain conversation is measured against its own framing, which carries
	// the user and their rules and so is not the one-sentence prompt the cast
	// path would measure for a chat with no character in it.
	if c.chat.Kind == store.KindAssistant || c.char.Name == "" {
		return scene.PlainBudget(c.cfg)
	}
	return scene.GroupBudget(c.cfg, c.sceneCast(), c.persona())
}

// warnIfCastTooLarge says so when the cast as a whole does not fit the window.
//
// The per-card warning next door catches one oversized description. A group
// fails a different way: five cards that each fit comfortably, and do not fit
// together. The symptom is the same and just as invisible — the server drops the
// front of the prompt, which is the framing — so it is worth the same
// interruption.
func (c *ChatView) warnIfCastTooLarge() {
	if !c.isGroup() {
		return
	}
	b := c.sceneBudget()
	if !b.Overflows {
		return
	}
	fixed := len(chars.BuildGroupSystem(c.cast, c.persona()))
	c.fail(fmt.Sprintf(
		"These %d characters do not fit the context size together. Their descriptions need about "+
			"%d tokens, and the window is %d. Use fewer of them, shorten a card, or raise the "+
			"context size in Settings.",
		len(c.cast), fixed/4, c.cfg.NumCtx))
}

// Cast is who is in this scene.
func (c *ChatView) Cast() []chars.Character { return c.cast }

// SetCast changes who is in a running scene, and returns whether it took.
//
// A character added mid-scene arrives knowing what is in the transcript and
// nothing else, which is the same position anyone walking into a room is in.
// One removed keeps every line they have already said: they left, they were
// never not there.
//
// Two is the floor once a scene has a cast. Narrowing to one would leave a
// transcript full of labelled replies in front of framing that never mentions
// labels, and the next reply would come back with a name typed into the prose
// where the splitter is no longer looking for one.
func (c *ChatView) SetCast(cast []chars.Character) bool {
	if c.busy {
		c.fail("Wait for this reply to finish before changing who is here.")
		return false
	}
	cast = trimNameless(cast)
	if c.isGroup() && len(cast) < 2 {
		c.fail("A scene with a cast needs at least two of them. Swap somebody out instead.")
		return false
	}
	if len(cast) == 0 {
		return false
	}

	// Becoming a group. The replies so far carry no speaker, because there was
	// only one person they could have been; they need the name they always
	// implied before a second voice appears above them.
	becoming := !c.isGroup() && len(cast) > 1
	if becoming && c.chat.ID != 0 && c.char.ID != 0 {
		if _, err := c.store.AttributeUnclaimed(c.chat.ID, c.char.ID); err != nil {
			c.fail("Could not attribute this scene's existing replies: " + err.Error())
			return false
		}
	}

	if c.chat.ID != 0 {
		ids := make([]int64, 0, len(cast))
		for _, member := range cast {
			ids = append(ids, member.ID)
		}
		if err := c.store.SetCast(c.chat.ID, ids); err != nil {
			c.fail("Could not save who is in this scene: " + err.Error())
			return false
		}
	}
	// Nothing is written for a scene that has not started; ensureChat records
	// the cast along with the chat when the first message goes out.
	c.cast = cast
	return true
}

// trimNameless drops members with no name, who cannot be labelled and so cannot
// be told apart in a reply.
func trimNameless(cast []chars.Character) []chars.Character {
	out := make([]chars.Character, 0, len(cast))
	for _, ca := range cast {
		if strings.TrimSpace(ca.Name) != "" {
			out = append(out, ca)
		}
	}
	return out
}

// castChip is the control for who is in a scene. It sits beside the direction
// chip for the same reason that one is there rather than in a menu: adding or
// dropping somebody is a thing you do while reading a reply.
func (c *ChatView) castChip() *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("chat-action-chip")
	if c.isGroup() {
		names := c.castNames()
		btn.SetLabel(strconv.Itoa(len(names)) + " here")
		btn.AddCSSClass("direction-set")
		btn.SetTooltipText(strings.Join(names, ", ") + "\n\nClick to add or remove someone.")
	} else {
		btn.SetLabel("Add someone")
		btn.SetTooltipText("Bring another character into this scene")
	}
	btn.ConnectClicked(func() {
		if c.OnEditCast != nil {
			c.OnEditCast()
		}
	})
	return btn
}

// compactable reports whether this conversation should keep a recap.
//
// A scene with a character and a plain conversation both should: each outgrows
// the window, and each loses its own beginning when it does. The designers
// should not. Their whole content is the material an extraction reads at the
// end, and folding the first half of an interview into notes would be summarising
// the answer before anyone asked for it.
func (c *ChatView) compactable() bool {
	switch c.chat.Kind {
	case store.KindDesigner, store.KindStyleDesigner, store.KindWorldDesigner:
		return false
	case store.KindAssistant:
		return true
	}
	return c.char.Name != ""
}
