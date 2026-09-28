"use strict";

// Astral, seen from a phone.
//
// The PC holds the library and runs the model; this is a screen for it. Every
// request carries the token this device was given when it paired, and the only
// thing kept locally is that token.

const TOKEN_KEY = "astral.token";
const $ = (id) => document.getElementById(id);

let token = localStorage.getItem(TOKEN_KEY) || "";
let state = { chats: [], characters: [], worlds: [] };
let current = null; // the open chat
let streaming = false;

// ---- Talking to the PC ----

async function api(path, opts = {}) {
	const res = await fetch(path, {
		...opts,
		headers: {
			"Content-Type": "application/json",
			Authorization: "Bearer " + token,
			...(opts.headers || {}),
		},
	});
	if (res.status === 401) {
		// Revoked, or paired against a different machine. Ask again rather
		// than leaving every screen mysteriously empty.
		token = "";
		localStorage.removeItem(TOKEN_KEY);
		showPairing();
		throw new Error("not paired");
	}
	if (!res.ok) {
		let msg = "that did not work";
		try { msg = (await res.json()).error || msg; } catch (_) {}
		throw new Error(msg);
	}
	return res;
}

// ---- Rendering a message ----

// escape runs first and always. Everything below inserts tags, so any angle
// bracket that came from a model has to stop being one before that happens.
function escape(s) {
	return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

const QUOTE = /"([^"\n]*)"|“([^”\n]*)”/g;
const MARKED = /\*\*([^*]+)\*\*|\*([^*]+)\*|_([^_<>]+)_/g;

// The ranges the author wrapped in asterisks or underscores.
//
// A quotation inside one of these is part of that narration rather than speech.
// Treating it as speech cuts the narration in two and leaves an unpaired
// asterisk at each end, which is how a reply came out with its markers showing
// and its italics starting in the wrong places, one paragraph right and the next
// wrong all the way down.
function markedSpans(s) {
	const out = [];
	for (const m of s.matchAll(MARKED)) out.push([m.index, m.index + m[0].length]);
	return out;
}

function within(spans, start, end) {
	return spans.some(([a, b]) => start >= a && end <= b);
}

// Rendering mirrors the desktop, which is a separate implementation of these
// rules in another language, so the two are compared against one corpus by the
// test in internal/serve. They have drifted twice, and both times the drift was
// invisible from inside either one.
//
// A message is rendered one line at a time, which is the only way these rules
// are safe to apply. A marker pairs with the next one of its kind, and the
// pattern between a pair crosses a blank line perfectly happily, so run over a
// whole message the opening asterisk of one paragraph would pair with the
// closing asterisk of another three paragraphs down, swallowing everything
// between them, quoted speech included, into a single run of narration.
//
// One unpaired asterisk anywhere then shifted every pairing after it, so a
// reply came out as alternating stretches of correct and broken italics all the
// way to the end.
function renderMarkup(text, inline) {
	// Trailing newlines go first, as on the desktop. A bubble is pre-wrapped,
	// so a reply ending in blank lines otherwise ends in blank space, and a
	// model ends one in blank lines fairly often.
	const lines = escape(text).replace(/\n+$/, "").split("\n");
	const out = [];
	let inCode = false;
	for (const line of lines) {
		if (line.trim().startsWith("```")) {
			inCode = !inCode; // the fence itself is dropped
			continue;
		}
		// One element per line rather than one spanning the block, which would
		// have to survive the newlines between them and renders the same.
		out.push(inCode ? "<code>" + line + "</code>" : renderLine(line, inline));
	}
	return out.join("\n");
}

// renderLine handles what a line is before it handles what is in it: its
// indent, whether it is a bullet, whether it is a heading.
function renderLine(line, inline) {
	let trimmed = line.replace(/^[ \t]+/, "");
	const indent = line.slice(0, line.length - trimmed.length);

	let bullet = "";
	if (/^[-*+] /.test(trimmed)) {
		bullet = "\u2022 ";
		trimmed = trimmed.slice(2);
	}
	let heading = false;
	while (trimmed.startsWith("#")) {
		heading = true;
		trimmed = trimmed.slice(1);
	}
	if (heading) trimmed = trimmed.replace(/^ +/, "");

	// Code spans are lifted out before any other rule runs and put back after.
	//
	// Replacing a span with <code>...</code> up front and calling that
	// protection leaves its contents sitting in the string, so every later rule
	// can still reach inside: an asterisk within `5 * 3` paired with one
	// outside it, and the emphasis opened inside the element and closed outside
	// it. A placeholder carries no markers, so nothing can pair across it, and
	// it hides a quotation mark inside a code span from the speech splitter as
	// well, which is also what anyone writing one would expect.
	const [body, code] = protectCode(trimmed);
	const content = restoreCode(inline(body), code);
	return indent + bullet + (heading ? "<strong>" + content + "</strong>" : content);
}

// The placeholder a code span stands in as. It carries no markers, and any
// already in the text is dropped first so nothing a model writes can be taken
// for one of ours; it is a control character with nothing to show for it.
const CODE_SENTINEL = "\u0000";

function protectCode(s) {
	s = s.replace(/\u0000/g, "");
	if (!s.includes("`")) return [s, []];
	const spans = [];
	const out = s.replace(/`([^`]+)`/g, (_, inner) => {
		spans.push("<code>" + inner + "</code>");
		return CODE_SENTINEL + (spans.length - 1) + CODE_SENTINEL;
	});
	return [out, spans];
}

function restoreCode(s, spans) {
	for (let i = 0; i < spans.length; i++) {
		s = s.replace(CODE_SENTINEL + i + CODE_SENTINEL, spans[i]);
	}
	return s;
}

// A model's prose: what is inside quotation marks is speech, and everything
// else is narration whether or not it was marked, because measured over long
// scenes it often is not.
function replyLine(s) {
	let out = "", last = 0;
	const marked = markedSpans(s);
	for (const m of s.matchAll(QUOTE)) {
		if (within(marked, m.index, m.index + m[0].length)) continue;
		out += narration(s.slice(last, m.index));
		out += m[0][0] + '<span class="speech">' + emphasise(m[0].slice(1, -1)) + "</span>" + m[0].slice(-1);
		last = m.index + m[0].length;
	}
	return out + narration(s.slice(last));
}

function narration(s) {
	if (!s.trim()) return s;
	const lead = s.slice(0, s.length - s.trimStart().length);
	const trail = s.slice(s.trimEnd().length);
	const inner = s.slice(lead.length, s.length - trail.length)
		// Bold survives inside narration, as it does on the desktop. Stripping
		// it here meant the same reply read differently on the two screens.
		.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
		// The asterisks were the author saying "this is narration", and all of
		// this is narration, so the markers come out rather than nesting a
		// second identical span inside the first.
		.replace(/\*([^*]+)\*/g, "$1")
		.replace(/_([^_<>]+)_/g, "$1");
	return lead + '<span class="narration">' + inner + "</span>" + trail;
}

// Your own words are rendered as you wrote them: what you marked is narration,
// what you did not is left alone.
function ownLine(s) {
	let out = "", last = 0;
	const marked = markedSpans(s);
	for (const m of s.matchAll(QUOTE)) {
		if (within(marked, m.index, m.index + m[0].length)) continue;
		out += asWritten(s.slice(last, m.index));
		out += m[0][0] + '<span class="speech">' + emphasise(m[0].slice(1, -1)) + "</span>" + m[0].slice(-1);
		last = m.index + m[0].length;
	}
	return out + asWritten(s.slice(last));
}

function asWritten(s) {
	return s
		// Bold first, or ** reads as two adjacent italic markers and the span
		// opens in the middle of its own delimiter, leaving a stray asterisk at
		// each end.
		.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
		.replace(/\*([^*]+)\*/g, '<span class="narration">$1</span>')
		.replace(/_([^_<>]+)_/g, '<span class="narration">$1</span>');
}

// emphasise applies the inline markers inside a line of speech.
//
// Speech was the one place they were never applied, on either screen: a reply
// written as "I said it was **drawn**" showed its asterisks in the middle of a
// sentence where every other marker worked. Inside speech a marked word is the
// speaker leaning on it, not the author stepping outside the quotation, so it
// is emphasis rather than the narration style.
function emphasise(s) {
	return s
		.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
		.replace(/\*([^*]+)\*/g, "<em>$1</em>")
		.replace(/_([^_<>]+)_/g, "<em>$1</em>");
}

// plainLine is ordinary Markdown, with none of the roleplay reading applied.
//
// A general chat and the three designers are conversations, not scenes. Read
// with the roleplay rules every sentence outside a quotation mark becomes
// narration, so an answer to a question arrived on the phone dimmed and
// italicised from end to end, with whatever it happened to quote in bold. The
// desktop has always chosen by the kind of chat; this is the phone doing the
// same.
function plainLine(s) {
	return s
		.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
		.replace(/\*([^*]+)\*/g, "<em>$1</em>");
}

// The kinds of chat that are conversations rather than scenes, named as the
// store names them.
const PLAIN_KINDS = new Set(["assistant", "designer", "style", "world"]);

// proseFor picks how a message body is read, which depends on the kind of chat
// and on who wrote it.
//
// Inferring narration from everything outside quotation marks exists to cover
// for a model that forgets its asterisks. That reasoning does not reach your
// own messages: you put the asterisks where you meant them.
function proseFor(role) {
	if (PLAIN_KINDS.has(current?.kind)) return plainLine;
	return role === "user" ? ownLine : replyLine;
}

function renderReply(text) {
	return renderMarkup(text, replyLine);
}

function renderOwn(text) {
	return renderMarkup(text, ownLine);
}

// render is what a bubble calls: the right reading for this chat and speaker.
function render(text, role) {
	return renderMarkup(text, proseFor(role));
}

// ---- Screens ----

const SCREENS = ["pair", "home", "chats", "cast", "settings", "chat"];

// The icons are the PC's own set, used as a CSS mask so they take the colour
// of whatever they sit in. Named here by their short name; the file is the
// window's, unchanged.
function paintIcons(root = document) {
	for (const el of root.querySelectorAll("[data-icon]")) {
		el.style.setProperty("--icon", `url("/icons/astral-${el.dataset.icon}-symbolic.svg")`);
	}
}

function show(name) {
	for (const id of SCREENS) $(id).hidden = id !== name;
	$("tabs").hidden = name === "pair";
	document.body.classList.toggle("in-chat", name === "chat");
	for (const tab of document.querySelectorAll(".tab")) {
		tab.classList.toggle("is-on", tab.dataset.screen === name);
	}
}

function showPairing() { show("pair"); $("pair-code").focus(); }

// ---- Settings ----

let settings = null;

async function loadSettings() {
	const res = await api("/api/settings");
	settings = await res.json();

	fillSelect($("set-model"), settings.models, settings.model, shortModel);
	fillSelect($("set-style"), settings.styles, settings.style);
	$("set-numctx").value = settings.num_ctx || "";
	$("set-numpredict").value = settings.num_predict || "";
	$("set-temperature").value = settings.temperature ?? "";
	$("set-persona").value = settings.persona || "";
	$("set-persona-note").value = settings.persona_note || "";
	$("set-device").textContent = "Paired as " + (settings.device || "this device");
	$("set-version").textContent = "Astral " + (settings.version || "?") + " on your PC";
	$("set-update").textContent = inApp()
		? "This app is version " + appVersion() + ". Your PC updates itself from Settings there."
		: "Opened in a browser, so there is no app to update. Your PC updates itself from Settings there.";
	$("set-update-app").hidden = !inApp();
}

// ---- Updating the app ----
//
// Only inside the Android app: a browser cannot install anything, and Android
// will not update a sideloaded app by itself. The PC is asked what the newest
// version is and then fetches it, so this works on a network with no way out.

function inApp() { return typeof window.AstralApp !== "undefined"; }
function appVersion() { try { return window.AstralApp.version(); } catch (_) { return "?"; } }

let pendingVersion = "";

async function checkForAppUpdate() {
	if (!inApp()) return;
	const btn = $("set-update-app");
	setUpdateRow("Checking…", "");
	try {
		const res = await api("/api/app/latest?have=" + encodeURIComponent(appVersion()));
		const info = await res.json();
		if (info.current) {
			setUpdateRow("The app is up to date", "Version " + info.have);
			pendingVersion = "";
			return;
		}
		pendingVersion = info.version;
		setUpdateRow("Install " + info.version, (info.notes || []).slice(0, 2).join(" · "));
	} catch (e) {
		setUpdateRow("Could not check", e.message);
	}
	btn.hidden = false;
}

function setUpdateRow(title, note) {
	$("set-update-title").textContent = title;
	$("set-update-note").textContent = note;
}

function startAppUpdate() {
	if (!pendingVersion) { checkForAppUpdate(); return; }
	if (!window.AstralApp.canInstall()) {
		// Android will not let an app install anything until you say so, per
		// app, on a settings page it has to be sent to.
		setUpdateRow("Allow installing, then press again",
			"Android needs permission before an app can install one");
		window.AstralApp.askToInstall();
		return;
	}
	setUpdateRow("Downloading…", "Through your PC");
	window.AstralApp.install(pendingVersion, token);
}

// Called by the app while the download runs, and if it fails.
window.astralUpdateProgress = (pct) => {
	setUpdateRow(pct >= 0 ? "Downloading… " + pct + "%" : "Downloading…", "Through your PC");
};
window.astralUpdateFailed = (msg) => {
	setUpdateRow("The update failed", msg);
	toast(msg);
};

function fillSelect(el, values, chosen, label = (v) => v) {
	el.replaceChildren();
	const all = values && values.length ? values.slice() : [];
	if (chosen && !all.includes(chosen)) all.unshift(chosen);
	for (const v of all) {
		const opt = document.createElement("option");
		opt.value = v;
		opt.textContent = label(v);
		if (v === chosen) opt.selected = true;
		el.append(opt);
	}
}

async function saveSettings() {
	const body = {
		model: $("set-model").value,
		style: $("set-style").value,
		persona: $("set-persona").value,
		persona_note: $("set-persona-note").value,
		num_ctx: Number($("set-numctx").value) || 0,
		num_predict: Number($("set-numpredict").value) || 0,
		temperature: Number($("set-temperature").value),
	};
	try {
		const res = await api("/api/settings", { method: "POST", body: JSON.stringify(body) });
		settings = await res.json();
		toast("Saved. Your PC is using these too.");
		loadState();
	} catch (e) {
		toast(e.message);
	}
}

async function forgetDevice() {
	try {
		await api("/api/forget", { method: "POST", body: "{}" });
	} catch (_) {
		// Revoked is revoked, whatever the answer was.
	}
	token = "";
	localStorage.removeItem(TOKEN_KEY);
	showPairing();
}

function toast(text) {
	const el = $("toast");
	el.textContent = text;
	el.hidden = false;
	clearTimeout(toast.timer);
	toast.timer = setTimeout(() => { el.hidden = true; }, 3200);
}

function row({ title, note, initial, primary, onClick }) {
	const btn = document.createElement("button");
	btn.className = "row" + (primary ? " row-primary" : "");
	if (initial) {
		const av = document.createElement("div");
		av.className = "avatar";
		av.textContent = initial;
		btn.append(av);
	}
	const text = document.createElement("div");
	text.className = "row-text";
	const t = document.createElement("div");
	t.className = "row-title";
	t.textContent = title;
	text.append(t);
	if (note) {
		const n = document.createElement("div");
		n.className = "row-note";
		n.textContent = note;
		text.append(n);
	}
	btn.append(text);
	btn.addEventListener("click", onClick);
	return btn;
}

// shortModel drops the publisher. "huihui_ai/qwen3.6-abliterated:27b" is
// mostly somebody's account name, and on a phone it was taking the line the
// greeting needed and pushing it to "Welcome back, ...".
// shortModel is a model's name as a person reads it: the last part of the
// path, without the packaging, with the tag set apart. A repository path like
// hf.co/someone/Model-GGUF:Q4_K_S is an address, and on a phone it is truncated
// long before the part that says which model it is.
function shortModel(m) {
	if (!m) return "no model";
	let name = m.slice(m.lastIndexOf("/") + 1);
	let tag = "";
	const colon = name.lastIndexOf(":");
	if (colon >= 0) {
		tag = name.slice(colon + 1);
		name = name.slice(0, colon);
	}
	name = name.replace(/[-_.]gguf$/i, "");
	return tag && tag !== "latest" ? name + " · " + tag : name;
}

function initialOf(name) {
	return (name || "?").trim().charAt(0).toUpperCase() || "?";
}

// ---- Home ----

async function loadState() {
	const res = await api("/api/state");
	state = await res.json();

	$("home-greeting").textContent = state.persona ? "Welcome back, " + state.persona : "Astral";
	$("home-model").textContent = shortModel(state.model);

	const start = $("home-start");
	start.replaceChildren();
	if (state.characters?.length) {
		start.append(row({
			title: "Play a Scene", note: "with someone from your cast", primary: true,
			onClick: () => show("cast"),
		}));
	}
	if (state.worlds?.length) {
		start.append(row({
			title: "Play in a World", note: "the model plays the place and whoever you meet",
			onClick: () => show("cast"),
		}));
	}
	start.append(row({ title: "General Chat", note: "answers, with the web and your knowledge to draw on", onClick: () => newChat({}) }));

	const recent = $("home-recent");
	recent.replaceChildren();
	const chats = state.chats || [];
	if (!chats.length) {
		const p = document.createElement("p");
		p.className = "muted";
		p.textContent = "Nothing yet.";
		recent.append(p);
	}
	for (const c of chats.slice(0, 6)) recent.append(chatRow(c));

	const all = $("chats-list");
	all.replaceChildren();
	for (const c of chats) all.append(chatRow(c));

	const cs = $("cast-characters");
	cs.replaceChildren();
	for (const c of state.characters || []) {
		cs.append(swipeable(
			row({
				title: c.name, note: c.note, initial: initialOf(c.name),
				onClick: () => newChat({ character_id: c.id }),
			}),
			c.name,
			async () => {
				await api("/api/characters/" + c.id, { method: "DELETE" });
				state.characters = state.characters.filter((x) => x.id !== c.id);
				toast(c.name + " deleted. Scenes you played with them are kept.");
			},
		));
	}
	const ws = $("cast-worlds");
	ws.replaceChildren();
	for (const w of state.worlds || []) {
		ws.append(swipeable(
			row({
				title: w.name, note: w.note, initial: initialOf(w.name),
				onClick: () => newChat({ world_id: w.id }),
			}),
			w.name,
			async () => {
				await api("/api/worlds/" + w.id, { method: "DELETE" });
				state.worlds = state.worlds.filter((x) => x.id !== w.id);
				toast(w.name + " deleted, with its lorebook.");
			},
		));
	}
}

// swipeable wraps a row so it can be pulled aside to reveal a delete button.
//
// Revealing a button rather than deleting on the gesture itself. A swipe is easy
// to make by accident while scrolling a list, and deleting a character is not
// undoable, so the gesture uncovers the decision and a tap makes it. The button
// then asks once more, because there is no dialog on this screen and a WebView
// cannot be relied on to show one.
//
// Pointer events rather than touch events, so it works the same under a finger
// and under a mouse.
function swipeable(inner, label, onDelete) {
	const wrap = document.createElement("div");
	wrap.className = "swipe";

	const action = document.createElement("button");
	action.className = "swipe-delete";
	action.textContent = "Delete";
	action.setAttribute("aria-label", "Delete " + label);
	wrap.append(action);

	const front = document.createElement("div");
	front.className = "swipe-front";
	front.append(inner);
	wrap.append(front);

	const OPEN = 88;      // how far aside the row sits when the button is showing
	const REVEAL = 40;    // past this on release, it stays open
	const SLOP = 10;      // below this it is a tap, or a scroll, and not a swipe
	let startX = 0, startY = 0, dx = 0, dragging = false, decided = false, open = false;
	// Declared before close() below, which resets it.
	let armed = false;

	const setX = (x) => { front.style.transform = x ? "translateX(" + x + "px)" : ""; };
	const close = () => { open = false; setX(0); armed = false; action.textContent = "Delete"; };

	front.addEventListener("pointerdown", (e) => {
		if (e.pointerType === "mouse" && e.button !== 0) return;
		startX = e.clientX; startY = e.clientY;
		dx = 0; dragging = true; decided = false;
	});
	front.addEventListener("pointermove", (e) => {
		if (!dragging) return;
		const mx = e.clientX - startX, my = e.clientY - startY;
		if (!decided) {
			if (Math.abs(mx) < SLOP && Math.abs(my) < SLOP) return;
			// Vertical wins: the list has to stay scrollable, and a gesture that
			// started as a scroll must not turn into a swipe halfway down.
			decided = true;
			if (Math.abs(my) > Math.abs(mx)) { dragging = false; return; }
			// Capturing keeps the moves coming when the finger leaves the row,
			// and is not worth failing the gesture over: a WebView that objects
			// to the capture still delivers the moves.
			try {
				front.setPointerCapture?.(e.pointerId);
			} catch (_) {}
			front.classList.add("dragging");
		}
		dx = Math.min(0, Math.max(-OPEN - 24, mx + (open ? -OPEN : 0)));
		setX(dx);
	});
	const end = () => {
		if (!dragging) return;
		dragging = false;
		front.classList.remove("dragging");
		if (!decided) return;
		if (dx <= -REVEAL) {
			// One at a time: a list with three rows hanging open is a list you
			// have lost track of.
			closeSwipes(wrap);
			open = true;
			setX(-OPEN);
		} else {
			close();
		}
	};
	front.addEventListener("pointerup", end);
	front.addEventListener("pointercancel", end);
	// A swipe that ended open must not also count as a tap on the row beneath it.
	front.addEventListener("click", (e) => {
		if (open || dx !== 0) { e.stopPropagation(); e.preventDefault(); close(); }
	}, true);

	action.addEventListener("click", async (e) => {
		e.stopPropagation();
		if (!armed) {
			armed = true;
			action.textContent = "Sure?";
			return;
		}
		try {
			await onDelete();
			wrap.remove();
		} catch (err) {
			toast(err.message);
			close();
		}
	});

	wrap.close = close;
	return wrap;
}

// openSwipes closes any row left pulled aside, so only one is ever open.
function closeSwipes(except) {
	for (const el of document.querySelectorAll(".swipe")) {
		if (el !== except && el.close) el.close();
	}
}

function chatRow(c) {
	return row({
		title: c.title || "Untitled",
		note: [c.who, c.messages ? c.messages + " messages" : ""].filter(Boolean).join(" · "),
		initial: initialOf(c.who || c.title),
		onClick: () => openChat(c.id),
	});
}

// ---- A conversation ----

async function newChat(body) {
	try {
		const res = await api("/api/chats", { method: "POST", body: JSON.stringify(body) });
		const { id } = await res.json();
		await openChat(id);
		loadState();
	} catch (e) {
		toast(e.message);
	}
}

async function openChat(id) {
	try {
		const res = await api("/api/chats/" + id);
		current = await res.json();
		$("chat-title").textContent = current.title || current.who || "Chat";
		const t = $("transcript");
		t.replaceChildren();
		for (const m of current.messages || []) t.append(bubble(m.role, m.content, m.who, m.accent, m.id));
		show("chat");
		scrollDown(false);
		$("composer-text").focus();
	} catch (e) {
		toast(e.message);
	}
}

// bubble is one turn. speaker names it when a scene has several characters in
// it, so a group reads as people talking rather than as one long reply; without
// one it falls back to the scene's single character, as it always did.
function bubble(role, content, speaker, accent, id) {
	const wrap = document.createElement("div");
	wrap.className = "msg" + (role === "user" ? " from-user" : "");
	if (id) wrap.dataset.id = id;
	wrap.dataset.role = role;
	const name = role === "user" ? "" : speaker || current?.who || "";
	if (name) {
		const who = document.createElement("div");
		who.className = "who";
		who.textContent = name;
		// The character's own tint, so five voices are distinguishable at a
		// glance and not only by reading the name above each one.
		if (typeof accent === "number" && accent > 0) {
			who.classList.add("who-accent-" + (accent % 4));
		}
		wrap.append(who);
	}
	const b = document.createElement("div");
	b.className = "bubble";
	b.innerHTML = render(content, role);
	// Tapping a turn offers what to do with it. Hidden until then, because a
	// row of buttons under every message is most of the screen on a phone, and
	// the desktop hides the same buttons until the pointer is over a row.
	b.addEventListener("click", (e) => {
		// Not when the tap was to select text or follow something inside it.
		if (window.getSelection()?.toString()) return;
		if (e.target.closest("a")) return;
		toggleActions(wrap);
	});
	wrap.append(b);
	return wrap;
}

// toggleActions shows or hides the action row under one turn, and closes any
// other that was open, so at most one is ever on screen.
function toggleActions(wrap) {
	const open = wrap.querySelector(".msg-actions");
	for (const row of document.querySelectorAll(".msg-actions")) row.remove();
	if (open) return;

	const row = document.createElement("div");
	row.className = "msg-actions";

	const add = (label, icon, danger, onClick) => {
		const b = document.createElement("button");
		b.className = "msg-action" + (danger ? " danger" : "");
		b.innerHTML = '<i class="tab-icon" data-icon="' + icon + '"></i>';
		b.setAttribute("aria-label", label);
		b.title = label;
		b.addEventListener("click", (e) => { e.stopPropagation(); onClick(); });
		row.append(b);
	};

	add("Copy this message", "copy", false, async () => {
		const text = wrap.querySelector(".bubble")?.innerText || "";
		try {
			await navigator.clipboard.writeText(text);
			toast("Copied.");
		} catch (_) {
			// A page served over plain http has no clipboard API in most
			// browsers, which is exactly how this one is served on a home
			// network. Selecting the text by hand still works.
			toast("This browser will not let a page copy. Hold the text to select it.");
		}
		row.remove();
	});

	// Only the last reply, and only a reply. Writing a turn again throws away
	// everything after it, which on a phone is one mis-tap away from losing a
	// scene, so the one turn it can safely mean is the one at the end.
	const last = $("transcript").lastElementChild;
	if (wrap.dataset.role === "assistant" && wrap === last) {
		add("Write this reply again", "regenerate", false, () => {
			row.remove();
			regenerate();
		});
	}

	// Two taps, as a swiped row asks: a turn deleted by a thumb that missed
	// Copy is a turn gone, and there is no undo.
	let armed = false;
	add("Delete this message", "trash", true, function () {
		const btn = row.querySelector(".msg-action.danger");
		if (!armed) {
			armed = true;
			btn.classList.add("armed");
			btn.textContent = "Sure?";
			btn.setAttribute("aria-label", "Tap again to delete this message");
			return;
		}
		row.remove();
		deleteMessage(wrap);
	});

	wrap.append(row);
	paintIcons(row);
}

async function deleteMessage(wrap) {
	const id = Number(wrap.dataset.id || 0);
	if (!current || streaming) return;
	if (!id) { wrap.remove(); return; }
	try {
		await api("/api/chats/" + current.id + "/messages/" + id, { method: "DELETE" });
		wrap.remove();
		current.messages = (current.messages || []).filter((m) => m.id !== id);
		loadState();
	} catch (e) {
		toast(e.message);
	}
}

function scrollDown(smooth = true) {
	const t = $("transcript");
	// Only follow if the reader is already at the end. Pulling someone back
	// down while they are reading earlier in the scene is the single most
	// irritating thing a transcript can do.
	if (!smooth && t.scrollHeight - t.scrollTop - t.clientHeight > 120) return;
	t.scrollTo({ top: t.scrollHeight, behavior: smooth ? "smooth" : "auto" });
}

async function send(text) {
	if (!current || streaming) return;
	$("transcript").append(bubble("user", text));
	await stream("/api/chats/" + current.id + "/send", JSON.stringify({ text }));
}

// regenerate throws away the last reply and asks for another.
//
// The whole of it: a group turn is several messages, one per speaker, so every
// reply at the end of the transcript goes, which is what undoes one turn rather
// than one voice within it. The server rewinds its own copy the same way.
async function regenerate() {
	if (!current || streaming) return;
	const t = $("transcript");
	while (t.lastElementChild && t.lastElementChild.dataset.role === "assistant") {
		t.lastElementChild.remove();
	}
	await stream("/api/chats/" + current.id + "/regenerate", "{}");
}

// stream runs one turn: it opens the row the reply is written into, consumes
// the event stream, and leaves the transcript as it will look when the scene
// is next opened.
async function stream(path, requestBody) {
	streaming = true;
	$("composer-send").disabled = true;

	const live = bubble("assistant", "");
	const body = live.querySelector(".bubble");
	body.classList.add("dots");
	$("transcript").append(live);
	scrollDown();

	let reply = "";
	let beats = null;
	// Plain text while it streams: half an asterisk is not markup.
	let painting = false;
	const paint = () => {
		if (painting) return;
		painting = true;
		requestAnimationFrame(() => {
			painting = false;
			body.textContent = reply;
			scrollDown(false);
		});
	};
	try {
		const res = await api(path, { method: "POST", body: requestBody });
		const reader = res.body.getReader();
		const decoder = new TextDecoder();
		let buffer = "";
		for (;;) {
			const { value, done } = await reader.read();
			if (done) break;
			buffer += decoder.decode(value, { stream: true });
			// Server-sent events arrive in blocks separated by a blank line,
			// and a block can be split across reads, so the tail stays in the
			// buffer until its terminator turns up.
			let cut;
			while ((cut = buffer.indexOf("\n\n")) >= 0) {
				const block = buffer.slice(0, cut);
				buffer = buffer.slice(cut + 2);
				const event = /^event: (.+)$/m.exec(block)?.[1] || "message";
				const data = /^data: (.+)$/m.exec(block)?.[1];
				if (!data) continue;
				const payload = JSON.parse(data);
				if (event === "token") {
					body.classList.remove("dots");
					if (!reply) body.textContent = "";
					reply += payload.t;
					// The text is written on the next frame rather than on
					// every event. Replacing it and scrolling per event makes
					// the browser lay the page out more often than it can
					// draw it, which on a phone is heat rather than speed.
					paint();
				} else if (event === "done") {
					// A group reply comes back already split by speaker. The
					// row it streamed into becomes the first beat and the rest
					// are appended, so the transcript ends up looking the same
					// as it will when the scene is reopened.
					beats = payload.beats || null;
					reply = payload.content || "";
					// The stored id, so this turn can be copied, deleted or
					// written again without reopening the scene first.
					if (payload.id) live.dataset.id = payload.id;
					if (payload.title) $("chat-title").textContent = payload.title;
				} else if (event === "searching") {
					// A search takes seconds with nothing arriving, so the row
					// says what is being looked up rather than sitting on dots.
					body.classList.remove("dots");
					reply = "";
					body.textContent = "Searching for " + payload.q + "…";
				} else if (event === "reading") {
					body.classList.remove("dots");
					reply = "";
					body.textContent = "Reading " + payload.url.replace(/^https?:\/\//, "") + "…";
				} else if (event === "reset") {
					// What streamed so far was the model deciding to search,
					// not the answer. The answer follows.
					reply = "";
					body.textContent = "";
				} else if (event === "error") {
					throw new Error(payload.error);
				}
			}
		}
		body.classList.remove("dots");
		if (beats && beats.length) {
			const who = live.querySelector(".who");
			if (who) {
				who.textContent = beats[0].who;
				if (beats[0].accent > 0) who.classList.add("who-accent-" + (beats[0].accent % 4));
			}
			body.innerHTML = render(beats[0].content, "assistant");
			if (beats[0].id) live.dataset.id = beats[0].id;
			for (const b of beats.slice(1)) {
				$("transcript").append(bubble("assistant", b.content, b.who, b.accent, b.id));
			}
		} else {
			body.innerHTML = render(reply, "assistant");
		}
		scrollDown();

		loadState();
	} catch (e) {
		body.classList.remove("dots");
		if (!reply) live.remove();
		toast(e.message);
	} finally {
		streaming = false;
		$("composer-send").disabled = false;
	}
}

// ---- Wiring ----

$("pair-go").addEventListener("click", async () => {
	const code = $("pair-code").value.trim().toUpperCase();
	const name = $("pair-name").value.trim() || "A phone";
	$("pair-error").textContent = "";
	try {
		const res = await fetch("/api/pair", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ code, name }),
		});
		if (!res.ok) {
			$("pair-error").textContent = (await res.json()).error || "that did not work";
			return;
		}
		token = (await res.json()).token;
		localStorage.setItem(TOKEN_KEY, token);
		await start();
	} catch (e) {
		$("pair-error").textContent = "Could not reach your PC. Same Wi-Fi?";
	}
});

$("chat-back").addEventListener("click", () => { current = null; show("home"); });

for (const tab of document.querySelectorAll(".tab")) {
	tab.addEventListener("click", () => {
		current = null;
		show(tab.dataset.screen);
		if (tab.dataset.screen === "settings") {
			loadSettings().then(checkForAppUpdate).catch((e) => toast(e.message));
		}
	});
}

$("set-save").addEventListener("click", saveSettings);
$("set-update-app").addEventListener("click", startAppUpdate);
$("set-forget").addEventListener("click", forgetDevice);

const composer = $("composer-text");
composer.addEventListener("input", () => {
	composer.style.height = "auto";
	composer.style.height = Math.min(composer.scrollHeight, window.innerHeight * 0.4) + "px";
});

$("composer").addEventListener("submit", (e) => {
	e.preventDefault();
	const text = composer.value.trim();
	if (!text) return;
	composer.value = "";
	composer.style.height = "auto";
	send(text);
});

// Enter sends on a keyboard, and makes a new line on a phone, where there is
// nowhere else for the return key to go.
composer.addEventListener("keydown", (e) => {
	if (e.key === "Enter" && !e.shiftKey && window.matchMedia("(min-width: 720px)").matches) {
		e.preventDefault();
		$("composer").requestSubmit();
	}
});

async function start() {
	paintIcons();
	if (!token) { showPairing(); return; }
	try {
		await loadState();
		// Astral opens on Home, here as well as on the PC.
		show("home");
	} catch (e) {
		if (token) toast(e.message);
	}
}

start();
