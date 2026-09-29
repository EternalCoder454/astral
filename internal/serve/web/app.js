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
// archivedOpen is whether the Archived entry on the Chats screen is showing
// the chats it holds.
let archivedOpen = false;
// The chats a reply is streaming into right now. Per chat, because leaving one
// mid-reply for another must not make the second think it is busy.
const streamingIn = new Set();
// waitingFor is the chat whose reply the PC is finishing after the stream to
// this phone was cut; see waitForReply.
let waitingFor = 0;
const busyHere = () => !!current && (streamingIn.has(current.id) || waitingFor === current.id);

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

// A scene is the one kind of chat whose prose is roleplay; every other kind is
// a conversation and reads as plain text. Named this way round because the
// list of conversation kinds grows: it was a list of those, and the Persona
// Creator and the Prompt Optimizer, added later, came out as italic
// narration from end to end.
const SCENE_KINDS = new Set(["roleplay", ""]);

// proseFor picks how a message body is read, which depends on the kind of chat
// and on who wrote it.
//
// Inferring narration from everything outside quotation marks exists to cover
// for a model that forgets its asterisks. That reasoning does not reach your
// own messages: you put the asterisks where you meant them.
function proseFor(role) {
	if (!SCENE_KINDS.has(current?.kind ?? "")) return plainLine;
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
	// Several personas: a choice of who you play as, which fills the name
	// and note below with that one's.
	const pick = $("set-persona-pick");
	const personas = settings.personas || [];
	$("set-persona-pick-field").hidden = personas.length < 2;
	pick.replaceChildren();
	for (const p of personas) {
		const opt = document.createElement("option");
		opt.value = String(p.id);
		opt.textContent = p.facts ? p.name + " (" + p.facts + ")" : p.name;
		pick.append(opt);
	}
	pick.value = String(settings.active_persona || "");
	pick.onchange = () => {
		const p = personas.find((x) => String(x.id) === pick.value);
		if (!p) return;
		$("set-persona").value = p.name;
		$("set-persona-note").value = p.note || "";
	};
	$("set-device").textContent = "Paired as " + (settings.device || "this device");
	$("set-version").textContent = "Astral " + (settings.version || "?") + " on your PC";
	$("set-update").textContent = inApp()
		? "This app is version " + appVersion() + "."
		: "Opened in a browser, so there is nothing to update here.";
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

// lastUpdateCheck is when GitHub was last asked, through the PC. Opening the
// Settings tab asked every time, and GitHub allows an address sixty questions
// an hour without an account.
let lastUpdateCheck = 0;

async function checkForAppUpdate(force = false) {
	if (!inApp()) return;
	if (!force && Date.now() - lastUpdateCheck < 30 * 60 * 1000) return;
	lastUpdateCheck = Date.now();
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
		offerUpdate(info);
	} catch (e) {
		setUpdateRow("Could not check", e.message);
	}
	btn.hidden = false;
}

function setUpdateRow(title, note) {
	$("set-update-title").textContent = title;
	$("set-update-note").textContent = note;
	// The popup, while it is open, says the same thing.
	if (!$("update-sheet").hidden) $("update-status").textContent = [title, note].filter(Boolean).join(" · ");
}

// LATER_KEY holds the version you said Later to, so the popup is not back
// every time the app opens; the next version asks again.
const LATER_KEY = "astral.updateLater";

// offerUpdate is the popup the desktop shows when there is a new version: what
// it brings, and one button to install it.
function offerUpdate(info) {
	if (!inApp() || !info?.version) return;
	try { if (localStorage.getItem(LATER_KEY) === info.version) return; } catch (_) {}
	$("update-title").textContent = "Astral " + info.version;
	const notes = $("update-notes");
	notes.replaceChildren();
	for (const n of (info.notes || []).slice(0, 8)) {
		const li = document.createElement("li");
		li.textContent = n;
		notes.append(li);
	}
	$("update-status").textContent = "";
	$("update-sheet").hidden = false;
}

function closeUpdate() { $("update-sheet").hidden = true; }

function startAppUpdate() {
	if (!pendingVersion) { checkForAppUpdate(true); return; }
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
	window.dispatchEvent(new Event("astral-update-failed"));
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
		...(!$("set-persona-pick-field").hidden && $("set-persona-pick").value
			? { active_persona: Number($("set-persona-pick").value) } : {}),
		// A box left empty means "leave it as it is", not zero. Sending zero
		// for an empty temperature box set the model to its most rigid.
		num_ctx: numberOrNull($("set-numctx").value),
		num_predict: numberOrNull($("set-numpredict").value),
		temperature: numberOrNull($("set-temperature").value),
	};
	try {
		const res = await api("/api/settings", { method: "POST", body: JSON.stringify(body) });
		settings = await res.json();
		toast("Saved.");
		loadState();
	} catch (e) {
		toast(e.message);
	}
}

function numberOrNull(v) {
	v = String(v ?? "").trim();
	if (v === "") return null;
	const n = Number(v);
	return Number.isFinite(n) ? n : null;
}

// forgetArmed is the first tap on Unpair. A second makes it happen: unpairing
// needs a new code from the PC to undo, and the button sat just under Save.
let forgetArmed = false;

async function forgetDevice() {
	if (!forgetArmed) {
		forgetArmed = true;
		$("set-forget").querySelector(".row-title").textContent = "Tap Again to Unpair";
		setTimeout(() => {
			forgetArmed = false;
			$("set-forget").querySelector(".row-title").textContent = "Unpair This Device";
		}, 4000);
		return;
	}
	forgetArmed = false;
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

function row({ title, note, initial, primary, onClick, picture, icon, tag = "button" }) {
	const btn = document.createElement(tag);
	btn.className = "row" + (primary ? " row-primary" : "");
	if (icon) {
		// An action rather than a person: its icon where a face would be.
		const av = document.createElement("div");
		av.className = "avatar icon-avatar";
		av.innerHTML = '<i class="tab-icon" data-icon="' + icon + '"></i>';
		paintIcons(av);
		btn.append(av);
	} else if (initial) {
		const av = document.createElement("div");
		av.className = "avatar";
		av.textContent = initial;
		// The character's own picture over the letter, once it has arrived.
		if (picture) showAvatar(picture, av);
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
	if (onClick) btn.addEventListener("click", onClick);
	return btn;
}

// avatarURLs holds each character's picture as a local URL, fetched once. It
// needs the pairing token, which a CSS url() cannot send.
const avatarURLs = new Map();

// showAvatar puts character id's picture in el, and leaves the letter if there
// is none or it cannot be fetched.
function showAvatar(id, el) {
	if (!avatarURLs.has(id)) {
		avatarURLs.set(id, api("/api/characters/" + id + "/avatar")
			.then((r) => (r.ok ? r.blob() : null))
			.then((b) => (b ? URL.createObjectURL(b) : null))
			.catch(() => null));
	}
	avatarURLs.get(id).then((url) => {
		if (!url) return;
		el.style.backgroundImage = `url("${url}")`;
		el.classList.add("has-picture");
	});
}

// hasPicture is whether character id has a picture to show.
function hasPicture(id) {
	return !!id && (state.characters || []).some((c) => c.id === id && c.avatar);
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
			onClick: () => { show("cast"); $("cast-worlds").previousElementSibling?.scrollIntoView({ block: "start" }); },
		}));
	}
	start.append(row({ title: "General Chat", note: "answers, with the web and your knowledge to draw on", onClick: () => newChat({}) }));

	const recent = $("home-recent");
	recent.replaceChildren();
	// Archived chats stay out of both lists until the Archived entry is opened.
	const chats = (state.chats || []).filter((c) => !c.archived);
	const archived = (state.chats || []).filter((c) => c.archived);
	if (!chats.length) {
		const p = document.createElement("p");
		p.className = "muted";
		p.textContent = "Nothing yet.";
		recent.append(p);
	}
	for (const c of chats.slice(0, 6)) recent.append(deletableChat(c));

	const all = $("chats-list");
	all.replaceChildren();
	for (const c of chats) {
		const r = deletableChat(c);
		r.dataset.find = [c.title, c.who].filter(Boolean).join(" ").toLowerCase();
		r.dataset.id = String(c.id);
		all.append(r);
	}
	if (archived.length) {
		const noteFor = () => (archivedOpen ? "Hide " : "") + archived.length + (archived.length === 1 ? " chat" : " chats");
		const entry = row({
			title: "Archived", note: noteFor(),
			onClick: () => {
				archivedOpen = !archivedOpen;
				entry.querySelector(".row-note").textContent = noteFor();
				filterChats();
			},
		});
		entry.dataset.archivedEntry = "1";
		all.append(entry);
		for (const c of archived) {
			const r = deletableChat(c);
			r.dataset.find = [c.title, c.who].filter(Boolean).join(" ").toLowerCase();
			r.dataset.id = String(c.id);
			r.dataset.archived = "1";
			all.append(r);
		}
	}
	filterChats();

	const ci = $("cast-import");
	ci.replaceChildren(row({
		title: "Import from a Link",
		note: "A Chub character page, or a card's .png or .json.",
		icon: "add",
		onClick: openImport,
	}));
	const cs = $("cast-characters");
	cs.replaceChildren();
	for (const c of state.characters || []) {
		cs.append(swipeable(
			row({
				title: c.favorite ? c.name + "  ★" : c.name, note: c.note, initial: initialOf(c.name),
				picture: c.avatar ? c.id : 0,
				onClick: () => newChat({ character_id: c.id }),
			}),
			c.name,
			async () => {
				await api("/api/characters/" + c.id, { method: "DELETE" });
				state.characters = state.characters.filter((x) => x.id !== c.id);
				toast(c.name + " deleted. Scenes you played with them are kept.");
				loadState().catch(() => {});
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
				loadState().catch(() => {});
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
	// The button is shown only while a swipe is uncovering it, and hidden
	// again once the row has slid back over it; see .revealing in style.css.
	const close = () => {
		open = false; setX(0); armed = false; action.textContent = "Delete";
		setTimeout(() => { if (!open && !dragging) wrap.classList.remove("revealing"); }, 220);
	};

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
			wrap.classList.add("revealing");
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

// deletableChat is a chat's row, pulled aside to delete it the same way a
// character is.
function deletableChat(c) {
	return swipeable(chatRow(c), c.title || "this chat", async () => {
		await api("/api/chats/" + c.id, { method: "DELETE" });
		toast("Chat deleted.");
		loadState().catch(() => {});
	});
}

// ago is when something last happened, as short as a list can say it.
function ago(unix) {
	if (!unix) return "";
	const s = Date.now() / 1000 - unix;
	if (s < 60) return "Now";
	if (s < 3600) return Math.floor(s / 60) + " min ago";
	if (s < 86400) return Math.floor(s / 3600) + " h ago";
	const d = new Date(unix * 1000);
	if (s < 2 * 86400) return "Yesterday";
	if (s < 6 * 86400) return d.toLocaleDateString(undefined, { weekday: "long" });
	return d.toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

function chatRow(c) {
	return row({
		title: c.title || "Untitled",
		note: [c.who, c.messages ? c.messages + " messages" : "", ago(c.updated)].filter(Boolean).join(" · "),
		initial: initialOf(c.who || c.title),
		picture: hasPicture(c.character) ? c.character : 0,
		onClick: () => openChat(c.id),
	});
}

// ---- A conversation ----

// startedHere is chats this page made. One left without a word from you is
// deleted on the way out: tapping a character to look was making a scene
// every time, and the list filled with greetings nobody answered.
const startedHere = new Set();

async function newChat(body) {
	try {
		const res = await api("/api/chats", { method: "POST", body: JSON.stringify(body) });
		const { id } = await res.json();
		startedHere.add(id);
		await openChat(id);
		loadState();
	} catch (e) {
		toast(e.message);
	}
}

async function openChat(id) {
	try {
		const res = await api("/api/chats/" + id);
		showChat(await res.json());
		// A history entry per open chat, so Back (the phone's own button
		// included) closes the chat rather than the app. The page had no
		// history at all, so the app's Back handler saw nothing to go back to
		// and quit.
		if (history.state?.chat !== id) history.pushState({ chat: id }, "");
		$("composer-text").focus();
	} catch (e) {
		toast(e.message);
	}
}

// showChat draws a chat as the PC has it.
function showChat(chat) {
	current = chat;
	$("chat-title").textContent = chat.title || chat.who || "Chat";
	const t = $("transcript");
	t.replaceChildren();
	// The latest messages only, and the rest on request: a scene played for
	// weeks drew every message it had before showing any, which grows with
	// the scene and on a phone is the wait between tapping it and reading.
	const msgs = chat.messages || [];
	const from = Math.max(0, msgs.length - SHOWN_AT_ONCE);
	for (const m of msgs.slice(from)) t.append(storedBubble(m));
	if (from > 0) t.prepend(earlierButton(msgs, from));
	if (!t.children.length) {
		// An empty chat said nothing at all, which on a phone reads as one
		// that failed to load.
		const hint = document.createElement("p");
		hint.className = "empty-chat";
		hint.textContent = chat.who && SCENE_KINDS.has(chat.kind ?? "")
			? "Start the scene with " + chat.who + "."
			: "Say something to begin.";
		t.append(hint);
	}
	showPortrait(chat);
	showChatPersona(chat);
	$("composer-draft").hidden = !chat.can_draft;
	refreshDraftButton();
	// Always: every chat can at least be archived from it.
	$("chat-more").hidden = false;
	show("chat");
	scrollDown(false);
	setComposerBusy(streamingIn.has(chat.id));
	if (chat.writing && !streamingIn.has(chat.id)) waitForReply(chat.id);
}

// SHOWN_AT_ONCE is how many messages a chat opens with, and how many more
// each press of Show Earlier Messages adds.
const SHOWN_AT_ONCE = 80;

// earlierButton draws the messages before end when pressed, keeping the one
// you were looking at where it was on screen.
function earlierButton(msgs, end) {
	const b = document.createElement("button");
	b.className = "earlier";
	b.textContent = "Show Earlier Messages";
	b.addEventListener("click", () => {
		const t = $("transcript");
		const from = Math.max(0, end - SHOWN_AT_ONCE);
		const kept = t.scrollHeight - t.scrollTop;
		const batch = document.createDocumentFragment();
		if (from > 0) batch.append(earlierButton(msgs, from));
		for (const m of msgs.slice(from, end)) batch.append(storedBubble(m));
		b.replaceWith(batch);
		t.scrollTop = t.scrollHeight - kept;
	});
	return b;
}

// showChatPersona puts who you are in this chat in the top bar, in a scene,
// when there is more than one of you to choose from.
function showChatPersona(chat) {
	const chip = $("chat-persona");
	chip.hidden = true;
	if (!SCENE_KINDS.has(chat.kind ?? "")) return;
	api("/api/settings").then((r) => r.json()).then((s) => {
		if (current?.id !== chat.id || (s.personas || []).length < 2) return;
		chip.textContent = "as " + (chat.persona || "You");
		chip.hidden = false;
		chip.onclick = () => pickChatPersona(chat, s.personas);
	}).catch(() => {});
}

// pickChatPersona lists your personas to play this chat as.
function pickChatPersona(chat, personas) {
	const list = $("persona-list");
	list.replaceChildren();
	for (const p of personas) {
		list.append(row({
			title: p.name + (p.id === chat.persona_id ? "  ✓" : ""),
			note: p.facts || "",
			initial: initialOf(p.name),
			onClick: async () => {
				try {
					const res = await api("/api/chats/" + chat.id + "/persona", {
						method: "POST", body: JSON.stringify({ persona_id: p.id }),
					});
					const out = await res.json();
					chat.persona_id = out.persona_id;
					chat.persona = out.persona;
					$("chat-persona").textContent = "as " + out.persona;
					toast("You are " + out.persona + " in this chat from the next turn.");
				} catch (e) {
					toast(e.message);
				}
				$("persona-sheet").hidden = true;
			},
		}));
	}
	$("persona-sheet").hidden = false;
}
$("persona-cancel").addEventListener("click", () => { $("persona-sheet").hidden = true; });

// portraits holds each chat's portrait as a local URL once fetched. The
// picture needs the pairing token to fetch, which a CSS url() cannot send, so
// it is fetched here and handed to the page as a blob.
const portraits = new Map();

// showPortrait sets the chat's character behind the conversation, when they
// have a portrait, and takes the last one away when they do not.
async function showPortrait(chat) {
	const screen = $("chat");
	if (!chat.portrait) {
		screen.classList.remove("has-portrait");
		return;
	}
	let url = portraits.get(chat.id);
	if (!url) {
		try {
			const res = await api("/api/chats/" + chat.id + "/portrait");
			if (!res.ok) throw new Error("no portrait");
			url = URL.createObjectURL(await res.blob());
			portraits.set(chat.id, url);
		} catch {
			screen.classList.remove("has-portrait");
			return;
		}
	}
	if (current?.id !== chat.id) return; // another chat was opened meanwhile
	screen.style.setProperty("--portrait", `url("${url}")`);
	screen.classList.add("has-portrait");
}

// leaveChat goes back to the lists, fetched again so what was said here, and
// anything started on the PC meanwhile, is in them.
function leaveChat() {
	stopSpeaking();
	const left = current;
	current = null;
	show("home");
	if (left && startedHere.has(left.id) && !streamingIn.has(left.id) &&
		!document.querySelector("#transcript .from-user")) {
		startedHere.delete(left.id);
		api("/api/chats/" + left.id, { method: "DELETE" })
			.catch(() => {})
			.finally(() => loadState().catch(() => {}));
		return;
	}
	loadState().catch(() => {});
}

// waitForReply is for a chat whose reply the PC is still writing, because the
// connection it was streaming over went away: the screen locked, or the phone
// left the Wi-Fi for a moment. The PC carries on and stores the reply, so the
// chat is asked again every couple of seconds until it has.
async function waitForReply(chatId) {
	if (waitingFor === chatId) return;
	waitingFor = chatId;
	const t = $("transcript");
	const note = bubble("assistant", "");
	note.querySelector(".bubble").classList.add("dots");
	note.dataset.waiting = "1";
	t.append(note);
	scrollDown();
	setComposerBusy(true);
	try {
		for (let i = 0; i < 450 && current?.id === chatId; i++) {
			await new Promise((r) => setTimeout(r, 2000));
			if (current?.id !== chatId || streamingIn.has(chatId)) return;
			let chat;
			try {
				chat = await (await api("/api/chats/" + chatId)).json();
			} catch (_) {
				continue; // still out of reach; keep waiting
			}
			if (!chat.writing) {
				nativeReply("done", chatId, chat.who || chat.title || "Astral", "");
				if (current?.id === chatId && !streamingIn.has(chatId)) showChat(chat);
				return;
			}
		}
	} finally {
		if (waitingFor === chatId) waitingFor = 0;
		note.remove();
		if (current?.id === chatId && !streamingIn.has(chatId)) setComposerBusy(false);
	}
}

// bubble is one turn. speaker names it when a scene has several characters in
// it, so a group reads as people talking rather than as one long reply; without
// one it falls back to the scene's single character, as it always did.
function bubble(role, content, speaker, accent, id, versions, version) {
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
	if (versions > 1) addPager(wrap, versions, version || 0);
	if (role !== "user") swipeVersions(wrap, b);
	return wrap;
}

// swipeVersions lets the last reply be swiped sideways, the way every other
// roleplay app does it: left for the next version, or past the newest for
// another one written, right for the one before. Only the last reply, as the
// arrows are only there, and only a clearly sideways swipe, so reading
// down the chat is never taken for one.
function swipeVersions(wrap, b) {
	let x0 = 0, y0 = 0, tracking = false, sideways = false;
	b.addEventListener("touchstart", (e) => {
		if (e.touches.length !== 1 || wrap !== $("transcript").lastElementChild || busyHere()) return;
		x0 = e.touches[0].clientX;
		y0 = e.touches[0].clientY;
		tracking = true;
		sideways = false;
	}, { passive: true });
	b.addEventListener("touchmove", (e) => {
		if (!tracking) return;
		const dx = e.touches[0].clientX - x0, dy = e.touches[0].clientY - y0;
		if (!sideways && Math.abs(dy) > 14 && Math.abs(dy) > Math.abs(dx)) { tracking = false; return; }
		if (Math.abs(dx) > 14 && Math.abs(dx) > Math.abs(dy) * 1.5) sideways = true;
		if (sideways) {
			b.style.transition = "none";
			b.style.transform = "translateX(" + Math.max(-60, Math.min(60, dx / 2)) + "px)";
		}
	}, { passive: true });
	const end = (e) => {
		if (!tracking) return;
		tracking = false;
		b.style.transition = "";
		b.style.transform = "";
		if (!sideways) return;
		const dx = (e.changedTouches?.[0]?.clientX ?? x0) - x0;
		if (Math.abs(dx) < 70) return;
		const v = wrap._versions;
		if (dx > 0) {
			if (v && v.at() > 0) v.go(v.at() - 1);
		} else if (v && v.at() < v.count - 1) {
			v.go(v.at() + 1);
		} else {
			regenerate();
		}
	};
	b.addEventListener("touchend", end);
	b.addEventListener("touchcancel", () => { tracking = false; b.style.transform = ""; });
}

// addPager puts the arrows under a reply that was written more than once,
// to go between the versions as the window's arrows do.
function addPager(wrap, versions, version) {
	wrap.querySelector(".pager")?.remove();
	const pager = document.createElement("div");
	pager.className = "pager";
	const prev = document.createElement("button");
	prev.textContent = "‹";
	prev.setAttribute("aria-label", "Previous version");
	const label = document.createElement("span");
	const next = document.createElement("button");
	next.textContent = "›";
	next.setAttribute("aria-label", "Next version");
	let at = version;
	const show = () => {
		label.textContent = (at + 1) + " / " + versions;
		prev.disabled = at === 0;
		next.disabled = at === versions - 1;
	};
	const go = async (to) => {
		if (busyHere() || to < 0 || to >= versions) return;
		try {
			const res = await api("/api/chats/" + current.id + "/messages/" + wrap.dataset.id + "/version", {
				method: "POST", body: JSON.stringify({ at: to }),
			});
			const v = await res.json();
			if (!res.ok) throw new Error(v.error || "That version could not be shown.");
			at = v.version;
			wrap.querySelector(".bubble").innerHTML = render(v.content, "assistant");
			const m = (current.messages || []).find((x) => String(x.id) === wrap.dataset.id);
			if (m) { m.content = v.content; m.version = at; }
			show();
		} catch (e) {
			toast(e.message);
		}
	};
	prev.addEventListener("click", (e) => { e.stopPropagation(); go(at - 1); });
	next.addEventListener("click", (e) => { e.stopPropagation(); go(at + 1); });
	// For a swipe on the reply itself; see swipeVersions.
	wrap._versions = { go, at: () => at, count: versions };
	pager.append(prev, label, next);
	show();
	wrap.append(pager);
}

// toggleActions shows or hides the action row under one turn, and closes any
// other that was open, so at most one is ever on screen.
function toggleActions(wrap) {
	const open = wrap.querySelector(".msg-actions");
	for (const row of document.querySelectorAll(".msg-actions")) row.remove();
	if (open) return;

	const row = document.createElement("div");
	row.className = "msg-actions";

	const add = (label, icon, danger, onClick, word) => {
		const b = document.createElement("button");
		b.className = "msg-action" + (danger ? " danger" : "");
		b.innerHTML = '<i class="tab-icon" data-icon="' + icon + '"></i>';
		const w = document.createElement("span");
		w.textContent = word;
		b.append(w);
		b.setAttribute("aria-label", label);
		b.title = label;
		b.addEventListener("click", (e) => { e.stopPropagation(); onClick(); });
		row.append(b);
	};

	add("Copy this message", "copy", false, async () => {
		const text = wrap.querySelector(".bubble")?.innerText || "";
		try {
			await copyText(text);
			toast("Copied.");
		} catch (_) {
			toast("This browser cannot copy, so hold the text to select it.");
		}
		row.remove();
	}, "Copy");

	// Only the last reply, and only a reply. Writing a turn again throws away
	// everything after it, which on a phone is one mis-tap away from losing a
	// scene, so the one turn it can safely mean is the one at the end.
	const last = $("transcript").lastElementChild;
	const isLastReply = wrap.dataset.role === "assistant" && wrap === last;
	if (isLastReply) {
		add("Write this reply again", "regenerate", false, () => {
			row.remove();
			regenerate();
		}, "Rewrite");
	}

	// Your last message, written better and answered again.
	const mineLast = [...$("transcript").querySelectorAll(".msg.from-user")].pop();
	if (wrap.dataset.role === "user" && wrap === mineLast && current?.can_draft && wrap.dataset.id) {
		add("Rewrite your message better, and have it answered again", "regenerate", false, () => {
			row.remove();
			rewriteMine(wrap);
		}, "Rewrite");
	}

	// The rest behind More: four buttons fit under a message on a phone, and
	// these are used less often than the ones on the row.
	const more = moreFor(wrap, isLastReply);
	if (more.length) {
		add("More for this message", "more", false, () => {
			row.remove();
			openMenu("This Message", more);
		}, "More");
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
	}, "Delete");

	wrap.append(row);
	paintIcons(row);
	// Under the last message the row opened behind the message box, where
	// nothing said it was there.
	row.scrollIntoView({ block: "nearest", behavior: "smooth" });
}

// copyText puts text on the clipboard.
//
// The clipboard API only exists on a secure page, and this one is served over
// plain http on a home network, so in the app it was never there and Copy
// always failed. The older way, copying a selection, is not held to that, so
// it is what a plain http page uses.
async function copyText(text) {
	if (navigator.clipboard && window.isSecureContext) {
		await navigator.clipboard.writeText(text);
		return;
	}
	const ta = document.createElement("textarea");
	ta.value = text;
	ta.setAttribute("readonly", "");
	ta.style.position = "fixed";
	ta.style.top = "0";
	ta.style.opacity = "0";
	document.body.append(ta);
	ta.select();
	ta.setSelectionRange(0, text.length);
	const ok = document.execCommand("copy");
	ta.remove();
	if (!ok) throw new Error("copy refused");
}

async function deleteMessage(wrap) {
	const id = Number(wrap.dataset.id || 0);
	if (busyHere()) return;
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
	if (!current || busyHere()) return;
	stopSpeaking();
	if (inApp() && typeof window.AstralApp.tick === "function") window.AstralApp.tick();
	document.querySelector("#transcript .empty-chat")?.remove();
	const mine = bubble("user", text);
	$("transcript").append(mine);
	// Writing in an archived chat brings it back, so the list here follows.
	const wrote = (state.chats || []).find((c) => c.id === current.id);
	if (wrote) wrote.archived = false;
	const outcome = await stream("/api/chats/" + current.id + "/send", JSON.stringify({ text }), mine);
	if (outcome === "refused") {
		// The PC never took it (busy, or out of reach), so nothing was saved.
		// The words go back where they were typed rather than being lost.
		mine.remove();
		const box = $("composer-text");
		if (!box.value.trim()) {
			box.value = text;
			box.dispatchEvent(new Event("input"));
		}
	}
}

// regenerate throws away the last reply and asks for another.
//
// The whole of it: a group turn is several messages, one per speaker, so every
// reply at the end of the transcript goes, which is what undoes one turn rather
// than one voice within it. The server rewinds its own copy the same way.
async function regenerate(note = "") {
	if (!current || busyHere()) return;
	const t = $("transcript");
	const old = [];
	for (let el = t.lastElementChild; el && el.dataset.role === "assistant"; el = el.previousElementSibling) {
		old.push(el);
	}
	// Hidden rather than removed until the PC has agreed: a refused
	// regenerate used to leave the screen without replies the PC still had.
	for (const el of old) el.hidden = true;
	const outcome = await stream("/api/chats/" + current.id + "/regenerate", JSON.stringify({ note }));
	for (const el of old) {
		if (outcome === "refused") el.hidden = false;
		else el.remove();
	}
}

// stopReply asks the PC to stop the reply being written into the open chat.
// What was written so far is kept, there and here.
async function stopReply() {
	if (!current) return;
	try {
		await api("/api/chats/" + current.id + "/stop", { method: "POST", body: "{}" });
	} catch (e) {
		toast(e.message);
	}
}

// setComposerBusy turns Send into Stop while a reply is being written, the
// way the window does, since that is the only time stopping means anything.
function setComposerBusy(on) {
	const btn = $("composer-send");
	btn.classList.toggle("is-stop", on);
	btn.setAttribute("aria-label", on ? "Stop" : "Send");
	const icon = btn.querySelector(".send-icon");
	icon.dataset.icon = on ? "stop" : "send";
	paintIcons(btn);
}

// stream runs one turn: it opens the row the reply is written into, consumes
// the event stream, and leaves the transcript as it will look when the scene
// is next opened. mine is the turn just sent, which is told its stored id.
//
// It answers how the turn went: "refused" when the PC did not take it at all
// (nothing was stored), "failed" for an error after it did, "dropped" when the
// connection went before the end (the PC finishes the reply regardless), and
// "done".
//
// Everything it draws is drawn only while the chat it started in is still the
// one on screen. Leaving a chat mid-reply used to carry on writing into
// whichever chat was opened next: its title, and in a group, its transcript.
async function stream(path, requestBody, mine) {
	const chatId = current.id;
	const here = () => current?.id === chatId;
	streamingIn.add(chatId);
	setComposerBusy(true);
	const who = current.who || current.title || "Astral";
	nativeReply("start", chatId, who);
	const settle = () => {
		streamingIn.delete(chatId);
		if (here()) setComposerBusy(false);
	};

	const live = bubble("assistant", "");
	const body = live.querySelector(".bubble");
	body.classList.add("dots");
	$("transcript").append(live);
	scrollDown();

	let reply = "";
	let beats = null;
	let versionsNow = 0, versionNow = 0;
	let finished = false;
	let stopped = false;
	// Plain text while it streams: half an asterisk is not markup.
	//
	// The last paint can still be waiting for its frame when the reply ends,
	// and the end draws the finished reply at once. The waiting paint then
	// ran after it and put the plain text back over the finished one, so a
	// reply kept its raw asterisks, no italics, until the chat was opened
	// again. final stops it.
	let painting = false;
	let final = false;
	const paint = () => {
		if (painting || final) return;
		painting = true;
		requestAnimationFrame(() => {
			painting = false;
			if (!here() || final) return;
			// Finished paragraphs are drawn as they will stay, italics and
			// all, and only the one still being written is plain text: the
			// whole reply used to stay plain until the end and then change
			// all at once, as the window's does not. A paragraph is finished
			// once a blank line follows it, so half an asterisk is never
			// read as markup.
			const cut = reply.lastIndexOf("\n\n");
			if (cut > 0) {
				body.innerHTML = render(reply.slice(0, cut), "assistant") + "\n\n" + escape(reply.slice(cut + 2));
			} else {
				body.textContent = reply;
			}
			scrollDown(false);
		});
	};

	let res;
	try {
		res = await api(path, { method: "POST", body: requestBody });
	} catch (e) {
		live.remove();
		toast(e.message);
		settle();
		return "refused";
	}

	let outcome = "done";
	try {
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
				if (event === "accepted") {
					// The turn just sent is stored, and this is its id, so it
					// can be deleted or copied without reopening the chat.
					if (mine && payload.user_id) mine.dataset.id = payload.user_id;
				} else if (event === "token") {
					body.classList.remove("dots");
					if (!reply) body.textContent = "";
					reply += payload.t;
					// The text is written on the next frame rather than on
					// every event. Replacing it and scrolling per event makes
					// the browser lay the page out more often than it can
					// draw it, which on a phone is heat rather than speed.
					paint();
				} else if (event === "done") {
					finished = true;
					stopped = !!payload.stopped;
					// A group reply comes back already split by speaker. The
					// row it streamed into becomes the first beat and the rest
					// are appended, so the transcript ends up looking the same
					// as it will when the scene is reopened.
					beats = payload.beats || null;
					reply = payload.content ?? reply;
					// The stored id, so this turn can be copied, deleted or
					// written again without reopening the scene first.
					if (payload.id) live.dataset.id = payload.id;
					versionsNow = payload.versions || 0;
					versionNow = payload.version || 0;
					if (payload.title && here()) $("chat-title").textContent = payload.title;
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
				} else if (event === "working") {
					// A tool other than search, such as saving to Knowledge.
					body.classList.remove("dots");
					reply = "";
					body.textContent = payload.say + "…";
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
		if (!finished) outcome = "dropped";
	} catch (e) {
		// A read that fails partway is the connection going, the same as one
		// that ends early; an error event is the PC saying something went
		// wrong, which is worth showing.
		outcome = e instanceof TypeError ? "dropped" : "failed";
		if (outcome === "failed" && here()) toast(e.message);
	}

	settle();
	final = true;
	body.classList.remove("dots");
	// Dropped means the PC is still writing: the app goes on waiting for it,
	// and announces it if the phone is put away before it lands.
	if (outcome !== "dropped") nativeReply(outcome === "done" ? "done" : "failed", chatId, who, reply);

	if (outcome === "dropped") {
		// The PC is still writing it, or has stored it. Asking is how to
		// find out which, and the chat is shown as the PC has it either way.
		live.remove();
		if (here()) waitForReply(chatId);
		return outcome;
	}
	if (outcome === "failed" || (stopped && !reply.trim() && !beats?.length)) {
		live.remove();
		return outcome;
	}
	if (here()) {
		if (beats && beats.length) {
			const who = live.querySelector(".who");
			if (who) {
				who.textContent = beats[0].who;
				if (beats[0].accent > 0) who.classList.add("who-accent-" + (beats[0].accent % 4));
			}
			body.innerHTML = render(beats[0].content, "assistant");
			if (beats[0].id) live.dataset.id = beats[0].id;
			const rest = [];
			for (const b of beats.slice(1)) {
				const el = bubble("assistant", b.content, b.who, b.accent, b.id);
				rest.push(el);
				$("transcript").append(el);
			}
			if (outcome === "done" && !stopped) readIfWanted(live, rest);
		} else {
			body.innerHTML = render(reply, "assistant");
			if (versionsNow > 1) addPager(live, versionsNow, versionNow);
			if (outcome === "done" && !stopped) readIfWanted(live);
		}
		scrollDown();
	}
	loadState().catch(() => {});
	return outcome;
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
		$("pair-error").textContent = "Could not reach your PC on this Wi-Fi.";
	}
});

// The on-screen Back goes through history too, so it and the phone's own Back
// button leave the same way and the history never holds a chat that is shut.
$("chat-back").addEventListener("click", () => {
	if (history.state?.chat) history.back();
	else leaveChat();
});
window.addEventListener("popstate", () => {
	if (current && !history.state?.chat) leaveChat();
	// Back from a chat opened out of another one, a branch, goes back to
	// that one. Without this Back did nothing at all once.
	else if (current && history.state.chat !== current.id) openChat(history.state.chat);
});

for (const tab of document.querySelectorAll(".tab")) {
	tab.addEventListener("click", () => {
		current = null;
		show(tab.dataset.screen);
		if (tab.dataset.screen === "settings") {
			showVoiceSettings();
			loadSettings().then(() => checkForAppUpdate()).catch((e) => toast(e.message));
		} else {
			// The lists as the PC has them now. They used to be fetched once,
			// so a scene started on the PC never appeared here.
			loadState().catch((e) => toast(e.message));
		}
	});
}

// Coming back to the app, after the screen was off or another app was in
// front, the lists and the open chat are fetched again: the PC may have
// finished a reply, or been used, in the meantime.
document.addEventListener("visibilitychange", () => {
	if (document.visibilityState !== "visible" || !token) return;
	loadState().catch(() => {});
	if (current && !busyHere()) {
		const id = current.id;
		api("/api/chats/" + id)
			.then((r) => r.json())
			.then((chat) => { if (current?.id === id && !busyHere()) showChat(chat); })
			.catch(() => {});
	}
});

// Settings save themselves as they change. The Save button sat between the
// fields and the device's own rows, so a change at the bottom meant scrolling
// back up to keep it, and one that was not saved looked saved.
let settingsTimer = 0;
for (const el of document.querySelectorAll("#settings input, #settings select, #settings textarea")) {
	// This phone's own preferences stay on this phone.
	if (el.dataset.local !== undefined) continue;
	el.addEventListener("change", () => {
		clearTimeout(settingsTimer);
		settingsTimer = setTimeout(saveSettings, 300);
	});
}

// filterChats hides the chats the search box does not match: their title or
// who they are with.
function filterChats() {
	const q = $("chats-search").value.trim().toLowerCase();
	let shown = 0;
	for (const r of $("chats-list").children) {
		// The Archived entry is for browsing, so a search has no use for it.
		if (r.dataset.archivedEntry) {
			r.hidden = !!q;
			continue;
		}
		// Only what was found for the words in the box now, never the last
		// search's while this one is on its way.
		const said = hitsFor === q ? searchHits.get(r.dataset.id) : undefined;
		const hit = !q || (r.dataset.find || "").includes(q) || said !== undefined;
		// The archive stays folded away until it is opened, unless a search is
		// looking for something in it.
		r.hidden = !hit || (!q && r.dataset.archived === "1" && !archivedOpen);
		if (!r.hidden) shown++;
		// A chat found by what was said in it shows the line that matched in
		// place of its usual note, which comes back when the box is cleared.
		const note = r.querySelector(".row-note");
		if (note) {
			if (note.dataset.usual === undefined) note.dataset.usual = note.textContent;
			note.textContent = q && said ? "“" + said + "”" : note.dataset.usual;
		}
	}
	$("chats-none").hidden = shown > 0 || !q;
}

// searchHits is what the PC's search found for the words in the box, by chat,
// with the line that matched. Asked a moment after typing stops, since it is a
// request to the PC and every letter would otherwise be one.
const searchHits = new Map();
let hitsFor = "";
let searchTimer = 0;
$("chats-search").addEventListener("input", () => {
	filterChats();
	clearTimeout(searchTimer);
	const q = $("chats-search").value.trim();
	if (q.length < 3) {
		searchHits.clear();
		filterChats();
		return;
	}
	searchTimer = setTimeout(async () => {
		try {
			const hits = await (await api("/api/search?q=" + encodeURIComponent(q))).json();
			if ($("chats-search").value.trim() !== q) return; // typed on since
			searchHits.clear();
			for (const h of hits) searchHits.set(String(h.id), h.snippet);
			hitsFor = q.toLowerCase();
			filterChats();
		} catch (_) {
			// Titles still filter; the search inside chats is a bonus.
		}
	}, 250);
});
$("update-go").addEventListener("click", () => {
	try { localStorage.removeItem(LATER_KEY); } catch (_) {}
	$("update-status").textContent = "Starting…";
	// Once: a second press would start a second download of the same file.
	$("update-go").disabled = true;
	startAppUpdate();
});
window.addEventListener("astral-update-failed", () => { $("update-go").disabled = false; });
$("update-later").addEventListener("click", () => {
	try { localStorage.setItem(LATER_KEY, pendingVersion); } catch (_) {}
	closeUpdate();
});
$("set-update-app").addEventListener("click", startAppUpdate);
$("set-forget").addEventListener("click", forgetDevice);

const composer = $("composer-text");
// The first keystroke after a pause asks the PC to load the model and read
// the chat, so a reply after a break starts in under a second instead of
// ten. Once a minute at most; the PC skips it when there is nothing to do.
let warmedAt = 0, warmedChat = 0;
composer.addEventListener("input", () => {
	if (current && composer.value && !busyHere() &&
		(warmedChat !== current.id || Date.now() - warmedAt > 60000)) {
		warmedAt = Date.now();
		warmedChat = current.id;
		api("/api/chats/" + current.id + "/warm", { method: "POST", body: "{}" }).catch(() => {});
	}
});
composer.addEventListener("input", () => {
	composer.style.height = "auto";
	composer.style.height = Math.min(composer.scrollHeight, window.innerHeight * 0.4) + "px";
});

$("composer").addEventListener("submit", (e) => {
	e.preventDefault();
	if (busyHere()) {
		stopReply();
		return;
	}
	const text = composer.value.trim();
	if (!text) return;
	composer.value = "";
	composer.style.height = "auto";
	refreshDraftButton();
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

// ---- Steering a scene ----

// storedBubble is a stored message as a turn on screen.
function storedBubble(m) {
	const el = bubble(m.role, m.content, m.who, m.accent, m.id, m.versions, m.version);
	if (m.pinned) el.classList.add("pinned");
	if (m.hidden) el.classList.add("hidden-msg");
	return el;
}

// moreFor is what a message's More offers.
function moreFor(wrap, isLastReply) {
	const items = [];
	const id = Number(wrap.dataset.id || 0);
	if (wrap.dataset.role === "assistant" && canSpeak()) {
		const reading = speaking === wrap;
		items.push({
			title: reading ? "Stop Reading" : "Read Aloud",
			note: reading ? "Stop the voice." : "Hear this reply in your phone's voice.",
			icon: reading ? "stop" : "speaker",
			onClick: () => (reading ? stopSpeaking() : speakReply(wrap)),
		});
	}
	if (isLastReply) {
		items.push({ title: "Rewrite with a Note", note: "Say what this reply should do differently.",
			icon: "draft", onClick: () => askNote() });
	}
	if (id && current?.remembers) {
		const pinned = wrap.classList.contains("pinned");
		items.push({
			title: pinned ? "Unpin" : "Pin",
			note: pinned ? "Stop keeping this one in mind." : "Keep this in mind however long the scene grows.",
			icon: "pin", onClick: () => pin(wrap, !pinned),
		});
	}
	if (id) {
		items.push({ title: "Branch from Here", note: "A new chat that is this one up to here.",
			icon: "branch", onClick: () => branch(id) });
		const hidden = wrap.classList.contains("hidden-msg");
		items.push({
			title: hidden ? "Show to the Model" : "Hide from the Model",
			note: hidden ? "The model reads it again." : "It stays here, and the model stops reading it.",
			icon: "hidden", onClick: () => hideMessage(wrap, !hidden),
		});
	}
	return items;
}

// openMenu shows a short list of things to do.
function openMenu(heading, items) {
	$("menu-heading").textContent = heading;
	const list = $("menu-list");
	list.replaceChildren();
	for (const it of items) {
		list.append(row({
			title: it.title,
			note: it.note || "",
			icon: it.icon,
			initial: it.initial,
			picture: it.picture,
			onClick: () => { $("menu-sheet").hidden = true; it.onClick(); },
		}));
	}
	$("menu-sheet").hidden = false;
}
$("menu-cancel").addEventListener("click", () => { $("menu-sheet").hidden = true; });

// Every sheet closes when the dimmed page around it is tapped, as Android's
// own sheets do.
for (const id of ["menu-sheet", "note-sheet", "memory-sheet", "persona-sheet"]) {
	$(id).addEventListener("click", (e) => { if (e.target === $(id)) $(id).hidden = true; });
}

// nativeReply tells the Android app a reply has started or ended, so it keeps
// the screen on while one is written, taps when it lands, and announces one
// that finishes while the phone is put away. A browser has none of this.
function nativeReply(what, chatId, who, text = "") {
	if (!inApp() || typeof window.AstralApp.replyStarted !== "function") return;
	try {
		if (what === "start") window.AstralApp.replyStarted(String(chatId), who, token);
		else window.AstralApp.replyEnded(what === "done", who, text || "");
	} catch (_) {}
}

// ---- Read Aloud ----

// READ_KEY and READ_WHAT_KEY are this phone's own choices: whether every reply
// is read as it finishes, and whether narration is read or only speech.
const READ_KEY = "astral.read";
const READ_WHAT_KEY = "astral.readWhat";
function pref(key, fallback) {
	try { return localStorage.getItem(key) || fallback; } catch (_) { return fallback; }
}
function setPref(key, value) {
	try { localStorage.setItem(key, value); } catch (_) {}
}

// speaking is the message being read, and speechId tells a finished reading
// from one that was replaced by the next.
let speaking = null;
let speechId = 0;

// canSpeak reports whether this phone can read aloud: the app, from the
// version that has a voice, or a browser that has one of its own.
function canSpeak() {
	if (inApp()) return typeof window.AstralApp.speak === "function";
	return "speechSynthesis" in window;
}

// spokenText is what is read of a reply: all of it, or only what is said
// aloud in quotation marks, as rendered, so no markup is read out.
function spokenText(wrap, more = []) {
	if (more.length) return [wrap, ...more].map((w) => spokenText(w)).filter(Boolean).join("\n\n");
	const body = wrap.querySelector(".bubble");
	if (!body) return "";
	if (pref(READ_WHAT_KEY, "all") === "speech") {
		const said = [...body.querySelectorAll(".speech")].map((e) => e.innerText.trim()).filter(Boolean);
		if (said.length) return said.join(" ");
	}
	return body.innerText.replace(/[*_]/g, "").trim();
}

function speakReply(wrap, more = []) {
	const text = spokenText(wrap, more);
	if (!text) return;
	stopSpeaking();
	speaking = wrap;
	wrap.classList.add("reading");
	const id = String(++speechId);
	if (inApp()) {
		window.AstralApp.speak(text, id);
		return;
	}
	const u = new SpeechSynthesisUtterance(text);
	u.onend = u.onerror = () => window.astralSpoke(id);
	speechSynthesis.speak(u);
}

function stopSpeaking() {
	if (!speaking) return;
	speaking.classList.remove("reading");
	speaking = null;
	speechId++;
	if (inApp()) window.AstralApp.stopSpeaking();
	else if ("speechSynthesis" in window) speechSynthesis.cancel();
}

// astralSpoke is called when a reading ends, by the app or the browser.
window.astralSpoke = (id) => {
	if (String(speechId) !== String(id) || !speaking) return;
	speaking.classList.remove("reading");
	speaking = null;
};

// readIfWanted reads a finished reply when this phone reads every reply.
function readIfWanted(wrap, more = []) {
	if (wrap && canSpeak() && pref(READ_KEY, "ask") === "every") speakReply(wrap, more);
}

$("set-read").addEventListener("change", () => setPref(READ_KEY, $("set-read").value));
$("set-read-what").addEventListener("change", () => setPref(READ_WHAT_KEY, $("set-read-what").value));

// showVoiceSettings shows the Read Aloud choices where there is a voice.
function showVoiceSettings() {
	const on = canSpeak();
	$("set-voice").hidden = !on;
	$("set-voice-heading").hidden = !on;
	$("set-read").value = pref(READ_KEY, "ask");
	$("set-read-what").value = pref(READ_WHAT_KEY, "all");
}

// REWRITE_NOTES are the notes asked for most, one tap each.
const REWRITE_NOTES = ["Shorter", "Longer", "More Dialogue", "More Detail", "Less Formal"];

// askNote asks what the last reply should do differently, then writes it again.
function askNote() {
	const text = $("note-text");
	text.value = "";
	$("note-go").disabled = true;
	const chips = $("note-chips");
	chips.replaceChildren();
	for (const n of REWRITE_NOTES) {
		const b = document.createElement("button");
		b.className = "chip chip-button";
		b.textContent = n;
		b.addEventListener("click", () => { $("note-sheet").hidden = true; regenerate(n); });
		chips.append(b);
	}
	$("note-sheet").hidden = false;
	text.focus();
}
$("note-text").addEventListener("input", () => { $("note-go").disabled = !$("note-text").value.trim(); });
$("note-cancel").addEventListener("click", () => { $("note-sheet").hidden = true; });
$("note-go").addEventListener("click", () => {
	const note = $("note-text").value.trim();
	if (!note) return;
	$("note-sheet").hidden = true;
	regenerate(note);
});

// pin pins or unpins one message.
async function pin(wrap, on) {
	if (!current) return;
	try {
		await api("/api/chats/" + current.id + "/messages/" + wrap.dataset.id + "/pin", {
			method: "POST", body: JSON.stringify({ pinned: on }),
		});
		wrap.classList.toggle("pinned", on);
		const m = (current.messages || []).find((x) => String(x.id) === wrap.dataset.id);
		if (m) m.pinned = on;
		toast(on ? "Pinned. The scene will keep it in mind." : "Unpinned.");
	} catch (e) {
		toast(e.message);
	}
}

// branch starts a new chat from this one up to a message, and opens it.
async function branch(id) {
	if (!current || busyHere()) return;
	try {
		const res = await api("/api/chats/" + current.id + "/branch", {
			method: "POST", body: JSON.stringify({ message_id: id }),
		});
		const out = await res.json();
		toast("Branched. The original is still in your chats.");
		await openChat(out.id);
		loadState().catch(() => {});
	} catch (e) {
		toast(e.message);
	}
}

// chatMenu is the open chat's More: its memory, and in a group, who is next.
function chatMenu() {
	if (!current) return;
	const items = [];
	if (current.can_draft) {
		items.push({ title: "Suggest Replies", note: "Three things you could say next.",
			icon: "ideas", onClick: suggestReplies });
	}
	if (current.remembers) {
		items.push({ title: "Scene Memory", note: "See and correct what this scene remembers.",
			icon: "history", onClick: openMemory });
		items.push({ title: "Continue in a New Chat", note: "The story so far, the pins and the last few messages, fresh.",
			icon: "branch", onClick: () => continueChat(current.id) });
	}
	if (current.can_draft) {
		const names = { "": "As the Style Says", short: "Short", medium: "Medium", long: "Long" };
		items.push({ title: "Reply Length", note: names[current.reply_length || ""] + ".",
			icon: "draft", onClick: chooseLength });
	}
	// One character's scene: a group's is named after its first member, and
	// starring them from there would star somebody at random.
	if (current.character && (current.cast || []).length < 2) {
		const fav = !!current.favorite;
		items.push({
			title: fav ? "Remove from Favorites" : "Add to Favorites",
			note: fav ? "Stop listing " + current.who + " first." : "List " + current.who + " first in the Cast.",
			icon: fav ? "star-outline" : "star",
			onClick: () => favorite(!fav),
		});
	}
	const cast = current.cast || [];
	if (cast.length > 1) {
		items.push({ title: "Let Them Talk", note: "They carry on among themselves, without you.",
			icon: "chat", onClick: () => groupTurn("") });
		for (const c of cast) {
			items.push({
				title: c.name + " Answers",
				note: "Sends your message with " + c.name + " answering, or has them speak now.",
				initial: initialOf(c.name),
				picture: c.avatar ? c.id : null,
				onClick: () => groupTurn(c.name),
			});
		}
	}
	const listed = (state.chats || []).find((c) => c.id === current.id);
	const away = !!listed?.archived;
	items.push({
		title: away ? "Unarchive" : "Archive",
		note: away ? "Put this chat back in your list." : "Move this chat out of your list.",
		icon: "history",
		onClick: () => archiveChat(current.id, !away),
	});
	openMenu(current.title || "This Chat", items);
}
$("chat-more").addEventListener("click", chatMenu);

// chooseLength offers the reply lengths a scene can ask for.
function chooseLength() {
	if (!current) return;
	const id = current.id;
	const options = [
		["", "As the Style Says", "Whatever length the writing style asks for."],
		["short", "Short", "One or two short paragraphs."],
		["medium", "Medium", "Two or three paragraphs."],
		["long", "Long", "Four to six full paragraphs."],
	];
	openMenu("Reply Length", options.map(([key, title, note]) => ({
		title: (current.reply_length || "") === key ? title + " (Now)" : title,
		note, icon: "draft",
		onClick: async () => {
			try {
				const res = await api("/api/chats/" + id + "/length", { method: "POST", body: JSON.stringify({ length: key }) });
				if (!res.ok) throw new Error((await res.json()).error || "That could not be done.");
				if (current && current.id === id) current.reply_length = key;
				toast("Replies are " + title.toLowerCase() + " from the next one on.");
			} catch (e) {
				toast(e.message);
			}
		},
	})));
}

// continueChat carries a chat on in a new one and opens it. The PC writes the
// story so far first, which for a long scene takes as long as the model needs
// to read it, so the wait is said out loud.
let continuing = false;
async function continueChat(id) {
	if (continuing) return;
	continuing = true;
	toast("Writing the story so far for the new chat…");
	try {
		const res = await api("/api/chats/" + id + "/continue", { method: "POST" });
		const out = await res.json();
		if (!res.ok) throw new Error(out.error || "That could not be done.");
		await loadState().catch(() => {});
		await openChat(out.id);
		toast("Continued in a new chat. The original is unchanged.");
	} catch (e) {
		toast("Could not continue it: " + e.message);
	} finally {
		continuing = false;
	}
}

// archiveChat puts a chat away, or brings it back.
async function archiveChat(id, archived) {
	try {
		const res = await api("/api/chats/" + id + "/archive", {
			method: "POST", body: JSON.stringify({ archived }),
		});
		if (!res.ok) throw new Error((await res.json()).error || "That could not be done.");
	} catch (e) {
		toast(e.message);
		return;
	}
	const c = (state.chats || []).find((x) => x.id === id);
	if (c) c.archived = archived;
	toast(archived ? "Chat archived." : "Chat brought back.");
	loadState().catch(() => {});
}

// hideMessage hides a message from the model, or shows it again.
async function hideMessage(wrap, on) {
	if (!current) return;
	try {
		await api("/api/chats/" + current.id + "/messages/" + wrap.dataset.id + "/hide", {
			method: "POST", body: JSON.stringify({ hidden: on }),
		});
		wrap.classList.toggle("hidden-msg", on);
		const m = (current.messages || []).find((x) => String(x.id) === wrap.dataset.id);
		if (m) m.hidden = on;
		toast(on ? "Hidden. The model no longer reads it." : "The model reads it again.");
	} catch (e) {
		toast(e.message);
	}
}

// favorite marks the chat's character as a favorite, or not.
async function favorite(on) {
	if (!current?.character) return;
	try {
		await api("/api/characters/" + current.character + "/favorite", {
			method: "POST", body: JSON.stringify({ favorite: on }),
		});
		current.favorite = on;
		toast(on ? current.who + " is a favorite." : current.who + " is no longer a favorite.");
		loadState().catch(() => {});
	} catch (e) {
		toast(e.message);
	}
}

// suggestReplies offers three things you could say next; one tapped goes in
// the message box, to send or change.
async function suggestReplies() {
	if (!current || busyHere()) return;
	const chatId = current.id;
	openMenu("Suggest Replies", [{ title: "Thinking of three…", note: "", icon: "ideas", onClick: () => {} }]);
	streamingIn.add(chatId);
	setComposerBusy(true);
	let options = null;
	try {
		const res = await api("/api/chats/" + chatId + "/suggest", { method: "POST", body: "{}" });
		options = (await res.json()).options || [];
	} catch (e) {
		$("menu-sheet").hidden = true;
		toast(e.message);
	} finally {
		streamingIn.delete(chatId);
		if (current?.id === chatId) setComposerBusy(false);
	}
	if (!options || current?.id !== chatId || $("menu-sheet").hidden) return;
	openMenu("Suggest Replies", options.map((o) => ({
		// Shown without its markup; what goes in the box keeps it.
		title: o.replace(/\*/g, ""), note: "", icon: "ideas",
		onClick: () => {
			const box = $("composer-text");
			box.value = o;
			box.dispatchEvent(new Event("input"));
			box.focus();
		},
	})));
}

// openImport asks for a link to a character.
function openImport() {
	$("import-url").value = "";
	$("import-sheet").hidden = false;
	$("import-url").focus();
}
$("import-cancel").addEventListener("click", () => { $("import-sheet").hidden = true; });
$("import-sheet").addEventListener("click", (e) => { if (e.target === $("import-sheet")) $("import-sheet").hidden = true; });
$("import-go").addEventListener("click", async () => {
	const url = $("import-url").value.trim();
	if (!url) return;
	const go = $("import-go");
	go.disabled = true;
	go.textContent = "Importing…";
	try {
		const res = await api("/api/characters/import", { method: "POST", body: JSON.stringify({ url }) });
		const out = await res.json();
		$("import-sheet").hidden = true;
		toast("Imported " + out.name + ".");
		await loadState();
	} catch (e) {
		toast(e.message);
	} finally {
		go.disabled = false;
		go.textContent = "Import";
	}
});

// groupTurn sends what is typed with somebody chosen to answer, or with
// nothing typed, has the cast carry on.
async function groupTurn(speaker) {
	if (!current || busyHere()) return;
	const box = $("composer-text");
	const text = box.value.trim();
	document.querySelector("#transcript .empty-chat")?.remove();
	let mine = null;
	if (text) {
		box.value = "";
		box.style.height = "auto";
		refreshDraftButton();
		mine = bubble("user", text);
		$("transcript").append(mine);
	}
	const outcome = await stream("/api/chats/" + current.id + "/send",
		JSON.stringify({ text, speaker, onward: !text }), mine);
	if (outcome === "refused" && mine) {
		mine.remove();
		if (!box.value.trim()) { box.value = text; box.dispatchEvent(new Event("input")); }
	}
}

// openMemory shows the scene's record, to read and correct, and its pins.

// showSeen lists what the next turn sends besides the conversation: the
// lorebook entries and what brought each in, and the earlier moments. The
// desktop's Scene Memory shows the same; see scene.WhatItSees.
function showSeen(seen) {
	const box = $("memory-seen");
	box.replaceChildren();
	const line = (text, muted) => {
		const p = document.createElement("p");
		p.className = muted ? "muted sheet-hint seen-line" : "seen-line";
		p.textContent = text;
		box.append(p);
	};
	const heading = (text) => {
		const h = document.createElement("h4");
		h.className = "seen-heading";
		h.textContent = text;
		box.append(h);
	};
	if (!seen) return;
	line("Sent with your next message, besides the conversation itself.", true);
	if (seen.world) {
		heading("From the Lorebook of " + seen.world);
		if (!seen.lore.length) line("No entries right now. They come in when the scene mentions them.", true);
		for (const l of seen.lore) {
			if (l.left) line(l.name + ": left out, no room. " + l.why, true);
			else line(l.name + ": " + l.why, false);
		}
	}
	if (!seen.record) {
		heading("Earlier Moments");
		line("None needed yet: the whole scene still fits in the model's memory.", true);
		return;
	}
	const snip = (t) => (t.length > 160 ? t.slice(0, 157).trimEnd() + "…" : t);
	if (seen.pinned.length) {
		heading("Pinned");
		for (const m of seen.pinned) line(m.who + ": " + snip(m.text), false);
	}
	heading("Recalled From Earlier");
	if (!seen.recalled.length) line("Nothing from before the record matches what is happening now.", true);
	for (const m of seen.recalled) line(m.who + ": " + snip(m.text), false);
}
async function openMemory() {
	if (!current) return;
	const chatId = current.id;
	let mem;
	try {
		mem = await (await api("/api/chats/" + chatId + "/memory")).json();
	} catch (e) {
		toast(e.message);
		return;
	}
	if (current?.id !== chatId) return;
	const recap = $("memory-recap");
	recap.value = mem.recap || "";
	recap.disabled = !mem.covers;
	$("memory-setting").value = mem.setting || "";
	$("memory-setting").dataset.was = mem.setting || "";
	$("memory-auto").checked = !!mem.setting_auto;
	$("memory-auto").dataset.was = mem.setting_auto ? "1" : "";
	showState(mem.fields || [], mem.state || {});
	const u = mem.usage;
	if (u) {
		const used = u.used + u.reply;
		const pct = Math.min(100, Math.round((used / Math.max(u.window, 1)) * 100));
		$("memory-usage-fill").style.width = pct + "%";
		const parts = (u.parts || []).map((p) => p.name + " " + p.tokens.toLocaleString()).join(", ");
		$("memory-usage").textContent = "About " + u.used.toLocaleString() + " of " + u.window.toLocaleString() +
			" tokens, " + pct + "% with the reply's room. " + parts + "." +
			(u.folds_at ? " The conversation folds into the record at " + u.folds_at.toLocaleString() +
				" tokens; it is at " + u.conversation.toLocaleString() + "." : "");
	}
	showSeen(mem.seen);
	$("memory-hint").textContent = mem.covers
		? "What the model is told about the part of the scene it can no longer see."
		: "Written once the scene outgrows the model's memory. Until then it reads every turn.";
	const pins = $("memory-pins");
	pins.replaceChildren();
	if (!mem.pins.length) {
		const p = document.createElement("p");
		p.className = "muted sheet-hint";
		p.textContent = "Nothing pinned. Tap a message, then More, to pin it.";
		pins.append(p);
	}
	for (const p of mem.pins) {
		// A plain row rather than a button: it holds a button of its own.
		const r = row({ title: p.who, note: p.text, initial: initialOf(p.who), tag: "div" });
		r.classList.add("pin-row");
		const un = document.createElement("button");
		un.className = "chip chip-button";
		un.textContent = "Unpin";
		un.addEventListener("click", async (e) => {
			e.stopPropagation();
			try {
				await api("/api/chats/" + chatId + "/messages/" + p.id + "/pin", {
					method: "POST", body: JSON.stringify({ pinned: false }),
				});
				r.remove();
				document.querySelector('#transcript .msg[data-id="' + p.id + '"]')?.classList.remove("pinned");
				const m = (current?.messages || []).find((x) => x.id === p.id);
				if (m) m.pinned = false;
			} catch (err) {
				toast(err.message);
			}
		});
		r.append(un);
		pins.append(r);
	}
	$("memory-sheet").hidden = false;
}
$("memory-cancel").addEventListener("click", () => { $("memory-sheet").hidden = true; });
// Writing your own line stops Astral rewriting it.
$("memory-setting").addEventListener("input", () => { $("memory-auto").checked = false; });
// showState builds a field for each part of the scene's state, labelled as
// the desktop labels it. See chars.SceneState.
function showState(fields, state) {
	const box = $("memory-state");
	box.replaceChildren();
	for (const f of fields) {
		const label = document.createElement("label");
		label.className = "sheet-label";
		label.textContent = f.label;
		const input = document.createElement("input");
		input.className = "sheet-field";
		input.type = "text";
		input.maxLength = 160;
		input.autocomplete = "off";
		input.placeholder = f.example || "";
		input.title = f.hint;
		input.dataset.key = f.key;
		input.value = state[f.key] || "";
		input.dataset.was = input.value;
		// Writing your own stops Astral rewriting them, as for the line above.
		input.addEventListener("input", () => { $("memory-auto").checked = false; });
		input.id = "memory-state-" + f.key;
		label.htmlFor = input.id;
		box.append(label, input);
	}
}

// stateFields are the state's inputs, by their part's key.
function stateFields() {
	return [...$("memory-state").querySelectorAll("input[data-key]")];
}

$("memory-suggest").addEventListener("click", async () => {
	if (!current) return;
	const b = $("memory-suggest");
	b.disabled = true;
	b.textContent = "Thinking…";
	try {
		const res = await api("/api/chats/" + current.id + "/setting/suggest", { method: "POST", body: "{}" });
		const out = await res.json();
		if (out.setting) $("memory-setting").value = out.setting;
		for (const input of stateFields()) {
			if (out.state && out.state[input.dataset.key]) input.value = out.state[input.dataset.key];
		}
		// A suggestion is still Astral's, so keeping it up to date stays on.
	} catch (e) {
		toast(e.message);
	} finally {
		b.disabled = false;
		b.textContent = "Suggest";
	}
});
$("memory-save").addEventListener("click", async () => {
	if (!current) return;
	const body = {};
	if (!$("memory-recap").disabled) body.recap = $("memory-recap").value;
	if ($("memory-setting").value.trim() !== ($("memory-setting").dataset.was || "")) body.setting = $("memory-setting").value;
	if ($("memory-auto").checked !== !!$("memory-auto").dataset.was) body.setting_auto = $("memory-auto").checked;
	const inputs = stateFields();
	if (inputs.some((i) => i.value.trim() !== (i.dataset.was || ""))) {
		body.state = Object.fromEntries(inputs.map((i) => [i.dataset.key, i.value.trim()]));
	}
	try {
		await api("/api/chats/" + current.id + "/memory", {
			method: "POST", body: JSON.stringify(body),
		});
		$("memory-sheet").hidden = true;
		toast("Saved, from your next message on.");
	} catch (e) {
		toast(e.message);
	}
});

// readEvents calls onEvent for each server-sent event in a response.
async function readEvents(res, onEvent) {
	const reader = res.body.getReader();
	const decoder = new TextDecoder();
	let buffer = "";
	for (;;) {
		const { value, done } = await reader.read();
		if (done) return;
		buffer += decoder.decode(value, { stream: true });
		let cut;
		while ((cut = buffer.indexOf("\n\n")) >= 0) {
			const block = buffer.slice(0, cut);
			buffer = buffer.slice(cut + 2);
			const event = /^event: (.+)$/m.exec(block)?.[1] || "message";
			const data = /^data: (.+)$/m.exec(block)?.[1];
			if (data) onEvent(event, JSON.parse(data));
		}
	}
}

// writeForMe drafts your next message into the message box, from what you had
// typed when there is anything. It is never sent for you. Send turns into
// Stop while it writes, as it does for a reply.
async function writeForMe() {
	if (!current || busyHere()) return;
	const chatId = current.id;
	const here = () => current?.id === chatId;
	const box = $("composer-text");
	const idea = box.value.trim();
	const was = box.placeholder;
	let text = "";
	streamingIn.add(chatId);
	setComposerBusy(true);
	$("composer-draft").disabled = true;
	box.value = "";
	box.placeholder = "Writing your message…";
	const put = (t) => {
		if (!here()) return;
		box.value = t;
		box.dispatchEvent(new Event("input"));
	};
	try {
		const res = await api("/api/chats/" + chatId + "/draft", {
			method: "POST", body: JSON.stringify({ idea }),
		});
		await readEvents(res, (event, payload) => {
			if (event === "draft" || event === "done") {
				text = payload.text || text;
				put(text);
			} else if (event === "error") {
				throw new Error(payload.error);
			}
		});
	} catch (e) {
		if (here() && !(e instanceof TypeError)) toast(e.message);
	} finally {
		streamingIn.delete(chatId);
		$("composer-draft").disabled = false;
		box.placeholder = was;
		if (here()) {
			setComposerBusy(false);
			put(text.trim() ? text : idea);
			box.focus();
		}
	}
}
$("composer-draft").addEventListener("click", writeForMe);

// The Write for Me button makes what you typed better when there is anything
// typed, and says so.
function refreshDraftButton() {
	const typed = !!$("composer-text").value.trim();
	const btn = $("composer-draft");
	btn.setAttribute("aria-label", typed ? "Rewrite what you typed, better" : "Write for Me");
	btn.title = btn.getAttribute("aria-label");
	btn.querySelector(".send-icon").dataset.icon = typed ? "regenerate" : "draft";
	paintIcons(btn);
}
$("composer-text").addEventListener("input", refreshDraftButton);

// rewriteMine has your last message written better, then answered again.
async function rewriteMine(wrap) {
	if (!current || busyHere()) return;
	const chatId = current.id;
	const body = wrap.querySelector(".bubble");
	streamingIn.add(chatId);
	setComposerBusy(true);
	body.classList.add("dots");
	let out = null;
	try {
		const res = await api("/api/chats/" + chatId + "/messages/" + wrap.dataset.id + "/rewrite", { method: "POST", body: "{}" });
		out = await res.json();
	} catch (e) {
		toast(e.message);
	} finally {
		body.classList.remove("dots");
		streamingIn.delete(chatId);
		if (current?.id === chatId) setComposerBusy(false);
	}
	if (!out || current?.id !== chatId) return;
	body.innerHTML = render(out.content, "user");
	const m = (current.messages || []).find((x) => String(x.id) === wrap.dataset.id);
	if (m) m.content = out.content;
	if (!out.changed) return;
	// Answered again when a reply follows it.
	if (wrap.nextElementSibling?.dataset.role === "assistant") regenerate();
	else toast("Rewritten.");
}

async function start() {
	paintIcons();
	if (!token) { showPairing(); return; }
	try {
		await loadState();
		// Astral opens on Home, here as well as on the PC.
		show("home");
		// A new app is offered as the window offers a new Astral: on opening,
		// quietly, and never more than once in half an hour.
		checkForAppUpdate().catch(() => {});
	} catch (e) {
		if (!token) return; // unpaired; the pairing screen is up
		// Home, with a way to try again, rather than a blank page and a toast
		// that goes away.
		show("home");
		const start_ = $("home-start");
		toast(e.message);
		start_.replaceChildren(row({
			title: "Could Not Reach Your PC",
			note: "Tap to try again.",
			primary: true,
			onClick: start,
		}));
	}
}

start();
