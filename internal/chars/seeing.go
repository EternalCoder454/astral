package chars

import "strings"

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

// seeingPerson is the part of the reading that is about people, shared by the
// design and plain framings.
const seeingPerson = `If there is a person, or anything humanlike (anime, drawn, painted, a 3D render, a doll, someone in costume), most of what you write is about them. Go through these in order and give each a concrete line:
1. What kind of image it is: photograph, anime or manga, western cartoon, digital painting, 3D render, sketch. It changes how everything after it should be read.
2. Apparent age as a range ("late twenties to early thirties"), gender presentation, and ethnicity or skin tone in plain words, as they read.
3. Face: shape, jaw, cheekbones, nose, lips, brows, and the eyes (shape, colour if it can be seen, how they are set). Freckles, scars, moles, makeup.
4. Hair: colour, length, texture, style, anything in it. Facial hair.
5. Build and height as far as the frame shows: slight, wiry, broad, soft, muscular, heavy. Posture and how they are holding themselves.
6. Expression, and where they are looking.
7. Clothing, layer by layer: each garment's colour, material, fit and condition, and the era, job or subculture it points to. Jewellery, glasses, piercings, tattoos, anything carried or worn.
8. Anything not human about them: ears, horns, tail, wings, unusual eyes or skin.
9. Setting, lighting and mood, briefly, and only as far as they add to the person.

Several people: each in turn, left to right, and how they stand in relation to one another.
No person: the subject, style, setting, colours and mood, with the same concreteness.

Say what is seen, not what is usually true. If something cannot be made out (eye colour in shadow, anything cropped out of frame), say so rather than guessing.
Any writing in the image: quote it exactly.
A character from a film, game, anime or book that you recognise: say who and from what, since that is useful. A photograph of a real person: describe them without naming them, as a new person with that look.
Describe everything plainly, whatever it shows. Nothing here is seen by anyone but the person who sent it.
No preamble and no offer at the end. Start with the description.`

// seeingForDesign frames the reading for a character design chat.
const seeingForDesign = `You are the eyes for a character designer who cannot see. Someone designing a character for a roleplay story has sent this image, and what you write is the only version of it the designer will ever have.

` + seeingPerson + `

After the description, add one short paragraph beginning "Reads as:" with the impression they give: what kind of person this looks like, what their clothes and bearing suggest about their life and temperament. That paragraph is interpretation and the only place interpretation goes.`

// seeingForChat frames the reading for a plain conversation.
const seeingForChat = `You are describing an image for someone who cannot see it. It was sent into a conversation, and what you write is the only version of it the conversation will have.

` + seeingPerson

// SeeingPrompt is the system prompt for the model that looks at a picture on
// another model's behalf. design says the chat is building a character.
func SeeingPrompt(design bool) string {
	if design {
		return seeingForDesign
	}
	return seeingForChat
}

// SeeingRequest is the message the picture is attached to. What the person
// wrote with it goes along, so a question about the picture ("what is she
// holding?") gets looked at first rather than lost in a general description.
func SeeingRequest(said string) string {
	said = strings.TrimSpace(said)
	if said == "" {
		return "Describe this image."
	}
	return "Describe this image. It was sent with the message below; if the message asks about something in it, cover that first.\n\nTheir message: " + said
}

// SeenImage is how a description from another model sits in the turn the
// picture was sent with, so the model being talked to knows it is the picture
// and whose reading of it it is.
func SeenImage(seer, description string) string {
	return "[The image, as described by " + seer + ":]\n" + strings.TrimSpace(description)
}
