# UI and UX review: Astral

You are reviewing the interface of a desktop application from screenshots. I
want an honest, specific critique. Please do not be polite about it.

## What the application is

Astral is a Linux desktop app for **roleplaying with AI characters**, running
entirely on the user's own machine through a local model server. You write as
yourself, and a character written by you (or imported) writes back. Scenes can
be set in a "world" that the app remembers facts about as you play.

It is a single-user, offline, personal application. There are no accounts, no
sharing, no collaboration, and no network beyond the local model server.

Built with GTK4 and libadwaita, so it follows GNOME conventions, but with a
deliberately moody custom palette rather than the default GNOME grey. A light
theme exists; every screenshot here is the dark one.

## Who uses it

One person, at a desk, for long sessions of creative writing. They are
comfortable with computers but are **not** necessarily familiar with AI
tooling, and they should not have to be. Assume they have never used a similar
app and nobody is available to explain anything to them.

## The screenshots

All at 1240x820 in the dark theme, against a seeded example database.

| File | Screen |
|---|---|
| `01-chat.png` | A roleplay scene in progress |
| `02-portrait.png` | The same scene with the character portrait panel open |
| `03-welcome.png` | The home screen, shown when no scene is open |
| `04-new-chat.png` | Starting something new |
| `05-characters.png` | The character list |
| `06-character-editor.png` | Creating or editing a character |
| `07-worlds.png` | The list of worlds |
| `08-world.png` | A single world |
| `09-lorebook.png` | A world's lorebook |
| `10-writing-styles.png` | Writing styles |
| `11-settings.png` | Settings |

## What I want from you

Work through these in order. Be concrete: name the screenshot, name the
element, and say what you would do instead.

**1. Can a newcomer find their way?**
Looking only at `03-welcome.png`, what would a first-time user do? Is the
primary action obvious? Trace the path from opening the app to being in a
scene, and say where someone would hesitate, guess, or click the wrong thing.

**2. What is confusing or unexplained?**
Point at anything whose purpose is not clear from the screen itself: labels,
icons, controls, or terms. Some of the vocabulary is domain jargon. Tell me
which words you had to infer, and which you could not.

**3. Is anything hidden that should not be?**
Features reachable only through a menu, an icon with no label, or a screen you
would only find by accident. Conversely, is anything prominent that does not
deserve to be?

**4. Visual design**
Hierarchy, spacing, alignment, density, contrast, and typography. Does the eye
land on the right thing first on each screen? Flag anything that reads as
decorative rather than informative, and anything hard to read.

**5. Consistency**
Compare the screens against each other. Do the same kinds of things look and
behave the same way? Are buttons, headers, dialogs, lists and cards
consistent? Flag anything that breaks a pattern the other screens establish.

**6. The reading experience**
`01-chat.png` and `02-portrait.png` are where most time is spent. The
transcript distinguishes narration from spoken dialogue. Does that work? Is a
long scene comfortable to read and skim? Can you tell at a glance who said
what?

**7. What is missing?**
Anything you expect in an app like this and cannot see.

## Ground rules

- **Assume nothing is intentional.** If something looks odd, say so. Do not
  give the benefit of the doubt.
- **Prioritise.** End with your top five changes in order of impact, and say
  why each one is worth doing before the others.
- **Separate fact from taste.** Mark each point as either a usability problem
  (someone will fail at something) or a preference (it would look better).
  Both are welcome; I want to know which is which.
- **Be specific about severity.** "Low contrast" is less useful than "the
  timestamp under each message is too faint to read at a glance".
- **Do not pad.** No summary of what the app does, no praise as preamble. If a
  screen is fine, say it is fine in one line and move on.

## Already addressed, so please look past it

A previous review raised these and they have been fixed. Comment only if you
think the fix is wrong or incomplete:

- Chat metadata and tag contrast
- User and character messages looking too similar
- Card text being cut off before the space ran out
- The portrait panel squeezing the transcript
- The model name cluttering the message box
- Welcome-screen actions not looking like buttons
- Lorebook toggles not aligning to a column
- Whether Settings' Save applied to one page or all of them
- No visible route into playing a world

## Output format

For each numbered section above, a short heading followed by your findings as
a list. Then the prioritised top five. Plain markdown, no preamble.
