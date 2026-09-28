package ui

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"astral/internal/chars"
	"astral/internal/store"
)

// Who you are in this chat.
//
// A chat remembers the persona it was started with, so a scene you played as a
// smuggler goes on calling you by the smuggler's name after you have made a
// knight your default. A chat from before there were several of you, or one
// whose persona was deleted, is played as whoever is in use by default.

// you is the name and description of the persona this chat is played as. The
// name is empty for an unnamed persona; callers that need something to call
// you use youName.
func (c *ChatView) you() (name, description string) {
	if c.youSet {
		return c.youProfile.Name, c.youProfile.Description()
	}
	return c.cfg.PersonaName, c.cfg.PersonaDescription
}

// youName is what the characters call you in this chat.
func (c *ChatView) youName() string {
	if name, _ := c.you(); name != "" {
		return name
	}
	return chars.DefaultPersonaName
}

// loadPersona finds the persona the chat on screen was started with, or the
// one in use by default for a chat that has none, whose picture the rows need
// as much as its name.
func (c *ChatView) loadPersona() {
	c.youProfile, c.youSet = chars.Profile{}, false
	id := c.chat.PersonaID
	if id == 0 {
		id = c.cfg.ActivePersona
	}
	if id != 0 && c.PersonaFor != nil {
		c.youProfile, c.youSet = c.PersonaFor(id)
	}
	c.refreshPersonaChip()
}

// youAvatar is your picture beside a message, or nil for the initial.
func (c *ChatView) youAvatar() gtk.Widgetter {
	if !c.youSet || c.youProfile.AvatarPath == "" {
		return nil
	}
	return NewPersonaAvatar(c.youProfile, avatarSize)
}

// recordPersona writes down who a chat that has just been created is played as.
func (c *ChatView) recordPersona() {
	id := c.cfg.ActivePersona
	if c.youSet {
		id = c.youProfile.ID
	}
	if id == 0 || c.chat.ID == 0 {
		return
	}
	c.chat.PersonaID = id
	if err := c.store.SetChatPersona(c.chat.ID, id); err != nil {
		c.fail("Could not save who you are in this chat: " + err.Error())
	}
}

// PlayAs makes this chat played as p from the next turn.
func (c *ChatView) PlayAs(p chars.Profile) {
	c.youProfile, c.youSet = p, true
	if c.chat.ID != 0 {
		c.chat.PersonaID = p.ID
		if err := c.store.SetChatPersona(c.chat.ID, p.ID); err != nil {
			c.fail("Could not save who you are in this chat: " + err.Error())
		}
	}
	c.refreshPersonaChip()
}

// refreshPersonaChip shows who you are beside the model, in a scene. The other
// kinds of chat are not played as anyone.
func (c *ChatView) refreshPersonaChip() {
	if c.personaBtn == nil {
		return
	}
	scene := c.chat.Kind == "" || c.chat.Kind == store.KindRoleplay
	c.personaBtn.SetVisible(scene && c.OnPickPersona != nil)
	c.personaBtn.SetLabel(c.youName())
	c.personaBtn.SetTooltipText("Click to play as someone other than " + c.youName())
}
