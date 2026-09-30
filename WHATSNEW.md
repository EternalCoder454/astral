# What's new in Astral

This file is what Astral shows you when an update is available. One version per
heading, a few plain lines each. The detailed history is in the git log.

## 0.5.31
- The sidebar can be dragged much narrower, down to 150 pixels; it used to stop at about 330

## 0.5.30
- A new look after Atlas Monitor: one title bar across the window, and the page as a layer inside the frame
- Characters, Worlds, Knowledge and Prompts are pages in the window now, and the sidebar marks where you are
- Nine themes in Settings, Appearance: Ink, Paper, Ember, Nord, Sage, Plum, Rose, Solarized and Contrast
- Settings folds into one column on a narrow window, and a theme applies the moment you pick it
- Every icon is now from Material Symbols, and each action has an icon of its own
- A polishing pass over narrow windows, empty screens, dialogs and delete buttons
- Phone: colours follow the desktop's theme, and chats can be renamed, deleted and edited
- Phone: Back returns to where you were, bigger touch targets, and settings that check their values

## 0.5.29
- The Replies chip sets how long a scene's replies are, and lets its character write first when you go quiet
- Memory finds earlier moments by what they are about as well as their words, with an embedding model
- In a group scene, someone who joined partway through no longer knows what was said before they came
- Lorebook entries can have a chance to appear, a wait before they do, and a group that sends only one
- Models that want the system prompt first, like some Qwen 3.5 downloads, now work in scenes
- Chats open in about half the time, and Astral hands back memory once you stop switching between them
- Phone: reply length and writing first too, from a chat's menu

## 0.5.28
- Scene Memory shows what the model sees: which lorebook entries go with your next message, and why
- Scene State keeps what everyone wears and holds, how things stand and what is unresolved, every turn
- Continue in a New Chat starts a long scene fresh, with its story so far, the pins and the last messages
- Archive chats you are done with, and group the sidebar by character with the button beside the search
- Phone: all of these too, from a chat's menu and its Scene Memory
- A chat with no character shows a small tile in the sidebar instead of a stretched bar

## 0.5.27
- Reading far back through a long scene no longer leaves it heavy: what you scrolled past is let go at the end
- In a group scene, earlier messages loaded by scrolling up show each speaker's name again

## 0.5.26
- Long sessions stay light: chats and dialogs you leave are now freed instead of piling up in memory
- Chats open about twice as fast, and scrolling back brings in earlier messages a few at a time, smoothly
- The sidebar shows each chat's character picture instead of a colored dot
- New characters and personas get their age, gender, race, occupation and appearance filled in reliably
- The profile at the bottom of the sidebar shows only your persona's name again

## 0.5.25
- The first reply after a break starts in under a second: typing gets the model and the scene ready first
- Stock phrase checking holds back fewer words, so replies start showing a little sooner
- Daily backups are compressed to about a third of their size
- Updates keep only the latest source and no leftover copy of the app, about 33 MB less on Linux
- Phone: long chats open faster over Wi-Fi, sent compressed

## 0.5.24
- Stock phrases like "a shiver ran down her spine" and "smirk" are cut out of replies as they are written
- Continue this reply no longer writes the whole reply out again on Gemma-based models such as SOMPOA
- Where and When keeps up with the scene by itself after each reply, until you write your own
- Phone: a reply that finishes while the phone is put away sends a notification, once the app updates
- Phone: the screen stays on while a reply is written, and a light tap says it has arrived
- Phone: swipe the last reply sideways to go between its versions, or past the newest for another

## 0.5.23
- Widening a narrowed window no longer opens an empty dark panel down the right side
- A sidebar or portrait you closed stays closed when the window is narrowed and widened again

## 0.5.22
- Narrowing the window folds the sidebar away as it should, instead of leaving a black strip down the side
- Long directions and model names shorten to fit, and a reply's buttons wrap, so the chat fits any width

## 0.5.21
- Suggest Replies offers three things you could say next, in your own voice, to send or change
- Scene Memory has Where and When, a line sent every turn that the model can suggest from the scene
- Scene Memory shows how full the model's memory is, and what with
- Hide a message from the model and keep it in the chat, for an aside or a turn that went wrong
- Star your favorite characters to list them first, on the desktop and the phone
- Import a character from a Chub page, or a link to any card's .png or .json
- A reply's rarer actions, Branch, Hide and Save to Knowledge, sit under More on the desktop

## 0.5.20
- Phone: Read Aloud in a reply's More, in your phone's own voice, once the app has updated
- Phone: Settings can read every reply as it arrives, and read only what the characters say aloud
- Lorebook entries bring in the entries they mention, so a name in one sends what is known about it

## 0.5.19
- Write for Me drafts your next message in your own voice and length, and never sends it for you
- Type something first and the same button rewrites it better; on your last message, Rewrite re-answers it too
- Rewrite with a Note writes a reply again toward Shorter, More Dialogue, "she refuses" or anything you type
- Characters stay in the moment you left them in, instead of walking you home and on into the bedroom
- Pin a message and the scene keeps it in mind for good; Memory shows the scene's record to correct
- Branch from Here starts a new chat from any message and leaves the original as it was
- Group scenes: choose who answers next, or Let Them Talk and they carry on without you
- All of it works on the phone, from a message's More and the new menu at the top of a chat

## 0.5.18
- Group scenes: the characters you speak to answer, not everyone at once every turn
- Restore a daily backup from Settings; your current library is kept beside it
- Phone: a rewritten reply keeps the old one, with arrows to go between them
- Phone: a scene's top bar shows who you are in it, and switches persona for that chat

## 0.5.17
- Phone: replies show their italics paragraph by paragraph as they arrive, not all at the end
- Phone: characters' pictures in the lists, and search finds chats by anything said in them
- Long chats open three times faster on the desktop, and older messages load as you scroll up
- General Chat's key rules are protected from the Prompt Optimizer

## 0.5.16
- Characters have Age, Gender, Race, Occupation and Relationship to You fields, filled by the designer
- New Persona in New Chat opens the Persona Creator, and the list there is labels only
- The persona editor lines up, and an empty picture shows a frame to fill

## 0.5.15
- The picture reader, tuned on the 4B Image Model, marks hidden parts as cannot be seen instead of guessing
- It never names a real person in a photograph

## 0.5.14
- The picture reader writes a labelled sheet: Name, Age, Gender, Race, Face, Hair, Body, Clothing and the rest
- In a design chat the sheet ends with Relationship and Reads As, ready to build a card from

## 0.5.13
- Revising a character, style or world keeps everything you did not ask to change
- General Chat searches the web for anything current, and every chat but a scene knows today's date
- Invented personas always get a real name, gender and race
- General Chat ends with the answer, without a stray suggestion tacked on
- Characters no longer call you "sister" out of nowhere

## 0.5.12
- Phone: message buttons say Copy, Rewrite and Delete, and settings save themselves as you change them
- Phone: search your chats, and a chat you open and leave without a word is not kept
- Phone: opening the app downloads almost nothing after the first time, and long scenes open fast
- Phone: Unpair asks twice, and a message no longer blocks the button under it

## 0.5.11
- Phone: swipe to delete stays open, and no red strip shows under rows
- Phone: replies keep their italics when they finish, and designer chats read as plain text
- Phone: a popup offers each new app version, and Settings text no longer runs off the edge
- Phone: portrait scenes show more of the picture, and you can switch personas in Settings
- Phone: the keyboard and the status bar no longer cover the app on Android 15
- Every hint on the desktop is one short sentence, and every heading a proper title

## 0.5.10
- Long text boxes scroll on their own, so typing at the end of a long writing style stays in view

## 0.5.9
- Personas can have a picture, shown beside your messages and under your name
- Files up to 200,000 characters, and designers widen their memory so a big file arrives whole
- The picture reader describes people head to foot, holds nothing back, and is editable in Prompts

## 0.5.8
- Personas: play as several people, each with a name, age, gender, race, appearance and background
- The Persona Creator builds one with you, the way the character designer builds a character
- Every chat remembers who you played it as, and the button beside the model changes it
- Switch personas from the menu under your name; Settings shows who is in use

## 0.5.7
- Character descriptions show names instead of {{char}} and {{user}}, when starting a scene and everywhere else

## 0.5.6
- Designers and plain chats read text files: drop a .md or .txt on the chat, paste it, or attach it
- A long paste goes with the message as a file instead of filling the message box

## 0.5.5
- Select chats with Ctrl or Shift and a click, then delete them all at once
- Deleting a chat no longer asks first: Undo puts it back
- Each chat in the list has a menu button when you point at it
- Dragging the sidebar wider keeps up with the pointer, where it lagged at half speed
- Rows and cards no longer blur for a moment when clicked
- On your phone: a character's portrait sits behind their scene, under see-through messages

## 0.5.4
- Optimize All: the Prompt Optimizer goes through every prompt with one model, and you review each rewrite
- The optimizer keeps every rule and measured phrase, and Save warns about slots, markers and dashes
- The designers stay short, ask two questions at most, and hand over to the button when you say build it
- Every chat but a scene can search the web, prefers reputable sources, and saves what it finds to Knowledge
- Long replies stream smoothly to the end, where they used to slow the window down as they grew
- The chat list redraws about eight times faster, and revising a style keeps what it is revising

## 0.5.3
- Prompt Optimizer: a design chat that improves any of Astral's prompts, or one of your own
- It can read every prompt Astral sends, and the last request of each kind exactly as it went out
- Prompts in the sidebar: read every prompt, change it by hand, or put the original back
- Your version of a prompt is used everywhere that prompt is sent
- Revising a writing style no longer turns into designing a new one after the first reply

## 0.5.2
- Writing a reply again keeps the old one: arrows under it flip between every version
- Search your chats by anything said in them, from the box over the chat list (Ctrl+F)
- A copy of your library is saved every day, and the last seven are kept
- A notification when a reply finishes while Astral is in the background, and Escape stops one
- On your phone: Back closes the chat, not the app, and Send turns into Stop
- On your phone: a reply is finished on your PC if the screen locks, and waits for you
- On your phone: Copy works, chats can be swiped away, and lists stay up to date

## 0.5.1
- Drop a picture straight from Files, or copy and paste it: both used to be refused every time
- A design chat takes pictures even when your model cannot see: a model that can reads them first
- Settings, Image Model chooses which model reads pictures, and only one is ever in memory
- HEIC, AVIF, JPEG XL, SVG and more are accepted, and phone photos come in the right way up
- The designer reads the people in a picture closely: age, face, hair, build, clothing, expression
- Plain chats take pictures too

## 0.5.0
- Knowledge: notes, saved pages and studied topics that General Chat and the designers draw on
- Web search is on, reads whole pages, and works without SearXNG by using DuckDuckGo
- Long scenes recall the details their recap dropped, and stop repeating the same phrases
- Characters talk to you as you, and swearing stays varied instead of turning into insults
- Adwaita Sans throughout, softer bubbles, tidier character cards, and a phone that matches
- Select and copy any part of a reply, and paste or drop an image into a design chat
- On your phone: copy, delete or write a reply again, and italics that stay right
- One model in video memory at a time, so Astral cannot freeze the desktop by stacking them

## 0.4.9
- A quoted word inside *narration* no longer breaks the italics around it
- Your own **bold** shows as bold on your phone
- General Chat answers straight, without the preamble and the bulleted headings

## 0.4.8
- Every character has a page now: their world, their scenes, and who they know
- Say how two characters know each other, and a scene with both of them is told
- Ask a designer to rewrite a character, a world or a style you already have
- Settings and the character editor show twice as much at once
- Two new character fields: Appearance, and how they talk
- Drag the sidebar to the width you want, and its buttons are centred
- Swipe a character or world aside on your phone to delete it

## 0.4.7
- The model can search the web in General Chat and the designers, never in a scene
- It decides when to look something up, and what it searched is shown above the reply
- Search runs through SearXNG, which you host yourself, so only the query leaves your machine

## 0.4.6
- Bring characters into a world scene: the place plays itself, the people play themselves
- Export a world and its whole lorebook to one file, and import one back
- Paste your notes and have the model turn them into lorebook entries

## 0.4.5
- A world designer: the model interviews you and writes the setting and its lorebook
- General chat keeps a record of itself instead of losing its own beginning
- Your standing rules and who you are now reach general chat too
- Every title, heading and button is capitalised consistently

## 0.4.4
- Standing rules: a list you switch on and off, instead of one box you retype
- Settings is three pages rather than five, and Updates is now About
- Sliders say which direction does what, and what the default is

## 0.4.3
- Play a scene with up to five characters at once: they react to each other, not just to you
- Each character speaks in their own message, with their own name and colour
- Not everyone speaks every turn, so a scene reads as a conversation rather than a roll call
- Add or remove a character while a scene is running, from the chip above the composer
- Exporting a group scene names who said what

## 0.4.2
- Nothing changes in the app. This build only marks a change to how Astral is built and tested
- Astral's tests now run in ninety seconds instead of sixteen minutes, so fixes reach a build sooner

## 0.4.1
- The phone interface is themed in Material, so it looks like an Android app rather than a page
- Opening a character on your phone shows their opening message, as the window does
- A model pulled while Astral is open now appears without restarting it
- Long scenes open about three times faster and hold less memory
- Replies stream more smoothly on a phone, especially with a fast model
- The Android app updates in place from now on: uninstall once to get onto this build

## 0.4.0
- Edit any message: the transcript is what the model reads, so fixing a line steers the scene
- Continue a reply that stopped at the reply limit, instead of rerolling it
- Export a scene as Markdown, recap included
- Characters offer every opening they were written with, not just the first
- Reasoning that arrives inside a reply is hidden as it streams, not tidied away afterwards
- Long scenes with large lorebooks build their prompt three times faster

## 0.3.1
- The Android app updates itself: check and install from its own settings
- Settings on your phone: model, context, reply limit, who you are, writing style
- The phone uses Astral's own icons, and the Android app uses its own mark
- Unpair a phone from the phone itself, for the one you are giving away

## 0.3.0
- Your phone can now use this machine's Astral: same library, same models, over your own network
- An Android app, and a phone layout with a bottom bar
- Worlds can be played directly: no character needed, the model plays the place
- Worlds gained Rules, for what is always true in them
- Astral opens on Home every time instead of dropping you into the last scene
- A reply that runs on without finishing a sentence is caught, not just one that repeats
- Models that write their thinking into the reply get it folded away instead of saved as the scene
- Your own messages are shown as you wrote them, not restyled

## 0.2.0
- Worlds are now somewhere you can play: open one to see who lives there and start a scene with them
- Scene direction: tell a scene where to go next, and it steers without saying it out loud
- A new look in both themes, and descriptions now read as descriptions whether or not the model marks them
- Long scenes stop losing the character: the context window is now divided properly
- Replies that collapse into repeating themselves are caught and stopped
- A smaller model can take over the background work, which makes your next reply arrive sooner
- Home in the sidebar, search in your characters, worlds and lorebooks
- Astral tells you when a model no longer fits in video memory, instead of just getting slower

## 0.1.0
- First release
