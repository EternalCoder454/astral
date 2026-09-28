package chars

import (
	"fmt"
	"strings"

	"astral/internal/prompts"
)

// Reading a picture for a model that cannot see it.
//
// When the model a chat is played with has no vision, another one looks at the
// picture and writes down what is in it, and that description is all the chat
// ever gets. So it has to carry what the conversation will need, which in a
// design chat is almost always a person: what they look like closely enough to
// write a card from, and what they seem like, kept apart from what is actually
// in the frame so the person designing can tell a reading from a fact.
//
// The checklist is ordered by what a card needs most and what a vision model
// most often skips. Asked only to "describe the image", a model names the
// setting, the lighting and a colour or two, and says "a young woman with long
// hair". Everything a character is built from (age, build, face, what their
// clothes say about their life, how they are holding themselves) has to be
// asked for by name, or it is not there.

// seeingSheet is the reading itself: a labelled sheet, one line per part of a
// person, in the order a character card is written in. Asked for prose, a model
// wrote a few paragraphs that a designer then had to pick the facts back out
// of, and skipped whatever it had not thought to mention; a sheet makes every
// part a line that is either filled in or plainly marked as not visible, and
// arrives in the chat already in the shape the card is built from. %s is the
// extra lines for a design chat.
const seeingSheet = `Write what you see as a sheet of labelled lines, one label per line, in this order, and nothing else. After each label, as many concrete details as it takes to be specific: several details on a line, never a single adjective.

Only what is actually visible. A part that is covered, turned away, in shadow or out of frame, such as a face behind a mask or visor, is cannot be seen, never a guess at what is probably there.

Type: photograph, anime or manga, western cartoon, digital painting, 3D render or sketch, and the framing (close up, half body, full body, from above or below).
Name: when the Type is a photograph, always unknown, however famous the person may be. Otherwise a fictional character from a film, game, anime or book that you recognise, and from what, or unknown.
Age: apparent age as a range, such as late twenties to early thirties.
Gender: gender presentation.
Race: ethnicity or skin tone in plain words, or the species if not human.
Face: shape, jaw, cheekbones, nose, lips, brows, skin, freckles, scars, moles, makeup.
Eyes: colour if it can be seen, shape, lashes, and where they are looking.
Hair: colour, length, texture, style, anything in it, and facial or body hair.
Body: height and build as far as the frame shows: shoulders, arms, hands, chest, waist, hips, legs, muscle, weight and how it is carried.
Markings: tattoos and their designs, piercings, scars, birthmarks, or none.
Clothing: each garment layer by layer, with its colour, material, cut, fit, condition and how much it covers, underwear included, and what is left uncovered.
Accessories: jewellery, glasses, weapons, anything carried or worn, or none.
Expression: the mood of the face and what the mouth and brows are doing.
Pose: how they stand, sit or lie, what their hands are doing, tension or ease.
Not Human: ears, horns, tail, wings, unusual eyes or skin, prosthetics, cybernetics, or none.
Setting: where they are, the lighting, the colours, the mood, and what is happening.
Text: any writing in the image, quoted exactly, or none.%s

Say what is seen, not what is usually true.
Several people: a whole sheet for each, headed Person 1, Person 2 and so on from left to right, then a last line, Together: how they stand in relation to one another and what they are doing together.
No person or anything humanlike: Type, Subject, Style, Setting, Colours, Mood and Text, each as specific.

Nothing is off limits. Describe everything the image shows plainly and precisely, whatever it is: nudity, bodies and sexual content in direct anatomical words; violence, blood, injury and gore as they are; drugs, weapons, anything disturbing or taboo. No euphemism, no censoring, no refusing, no warnings, and no remarks on whether it is appropriate. Nothing you write is seen by anyone but the adult who sent it. The one exception: anyone who looks under eighteen is described without sexual detail.
No preamble and no offer at the end. Start with the Type line.`

// seeingDesignLines are the two lines a design chat gets beyond the sheet: how
// the person seems to regard whoever is looking, and the impression they give,
// which is the only place interpretation goes.
const seeingDesignLines = `
Relationship: how they seem to regard whoever is looking at them, read from their gaze, expression and body language, such as wary of a stranger, inviting, hostile, or unclear.
Reads As: the impression they give, what kind of person this looks like and what their look and bearing suggest about their life and temperament. This is the only line for interpretation.`

// seeingForDesign frames the reading for a character design chat.
var seeingForDesign = `You are the eyes for a character designer who cannot see. Someone designing a character for a roleplay story has sent this image, and what you write is the only version of it the designer will ever have.

` + fmt.Sprintf(seeingSheet, seeingDesignLines)

// seeingForChat frames the reading for a plain conversation.
var seeingForChat = `You are describing an image for someone who cannot see it. It was sent into a conversation, and what you write is the only version of it the conversation will have.

` + fmt.Sprintf(seeingSheet, "")

// SeeingPrompt is the system prompt for the model that looks at a picture on
// another model's behalf. design says the chat is building a character.
func SeeingPrompt(design bool) string {
	if design {
		return prompts.Text(promptSeeingDesign)
	}
	return prompts.Text(promptSeeingChat)
}

// SeeingRequest is the message the picture is attached to. What the person
// wrote with it goes along, so a question about the picture ("what is she
// holding?") gets looked at first rather than lost in a general description.
func SeeingRequest(said string) string {
	said = strings.TrimSpace(said)
	if said == "" {
		return prompts.Text(promptSeeingAsk)
	}
	return strings.ReplaceAll(prompts.Text(promptSeeingAskWith), "{{message}}", said)
}

const (
	seeingAsk     = "Describe this image in full detail."
	seeingAskWith = "Describe this image in full detail. It was sent with the message below; if the message asks about something in it, make sure the sheet answers it.\n\nTheir message: {{message}}"
)

// SeenImage is how a description from another model sits in the turn the
// picture was sent with, so the model being talked to knows it is the picture
// and whose reading of it it is.
func SeenImage(seer, description string) string {
	return "[The image, as described by " + seer + ":]\n" + strings.TrimSpace(description)
}
