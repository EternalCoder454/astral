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

// A model's prose: what is inside quotation marks is speech, and everything
// else is narration whether or not it was marked, because measured over long
// scenes it often is not.
function renderReply(text) {
	let out = "", last = 0;
	const s = escape(text);
	for (const m of s.matchAll(QUOTE)) {
		out += narration(s.slice(last, m.index));
		out += m[0][0] + '<span class="speech">' + m[0].slice(1, -1) + "</span>" + m[0].slice(-1);
		last = m.index + m[0].length;
	}
	return out + narration(s.slice(last));
}

function narration(s) {
	if (!s.trim()) return s;
	const lead = s.slice(0, s.length - s.trimStart().length);
	const trail = s.slice(s.trimEnd().length);
	const inner = s.slice(lead.length, s.length - trail.length)
		.replace(/\*\*([^*]+)\*\*/g, "$1")
		.replace(/\*([^*]+)\*/g, "$1")
		.replace(/_([^_]+)_/g, "$1");
	return lead + '<span class="narration">' + inner + "</span>" + trail;
}

// Your own words are rendered as you wrote them: what you marked is narration,
// what you did not is left alone.
function renderOwn(text) {
	let out = "", last = 0;
	const s = escape(text);
	for (const m of s.matchAll(QUOTE)) {
		out += asWritten(s.slice(last, m.index));
		out += m[0][0] + '<span class="speech">' + m[0].slice(1, -1) + "</span>" + m[0].slice(-1);
		last = m.index + m[0].length;
	}
	return out + asWritten(s.slice(last));
}

function asWritten(s) {
	return s
		.replace(/\*([^*]+)\*/g, '<span class="narration">$1</span>')
		.replace(/_([^_]+)_/g, '<span class="narration">$1</span>');
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

	fillSelect($("set-model"), settings.models, settings.model);
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

function fillSelect(el, values, chosen) {
	el.replaceChildren();
	const all = values && values.length ? values.slice() : [];
	if (chosen && !all.includes(chosen)) all.unshift(chosen);
	for (const v of all) {
		const opt = document.createElement("option");
		opt.value = v;
		opt.textContent = v;
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
function shortModel(m) {
	if (!m) return "no model";
	const cut = m.lastIndexOf("/");
	return cut >= 0 ? m.slice(cut + 1) : m;
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
			title: "Play a scene", note: "with someone from your cast", primary: true,
			onClick: () => show("cast"),
		}));
	}
	if (state.worlds?.length) {
		start.append(row({
			title: "Play in a world", note: "the model plays the place and whoever you meet",
			onClick: () => show("cast"),
		}));
	}
	start.append(row({ title: "General chat", note: "", onClick: () => newChat({}) }));

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
		cs.append(row({
			title: c.name, note: c.note, initial: initialOf(c.name),
			onClick: () => newChat({ character_id: c.id }),
		}));
	}
	const ws = $("cast-worlds");
	ws.replaceChildren();
	for (const w of state.worlds || []) {
		ws.append(row({
			title: w.name, note: w.note, initial: initialOf(w.name),
			onClick: () => newChat({ world_id: w.id }),
		}));
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
		for (const m of current.messages || []) t.append(bubble(m.role, m.content, m.who, m.accent));
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
function bubble(role, content, speaker, accent) {
	const wrap = document.createElement("div");
	wrap.className = "msg" + (role === "user" ? " from-user" : "");
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
	b.innerHTML = role === "user" ? renderOwn(content) : renderReply(content);
	wrap.append(b);
	return wrap;
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
	streaming = true;
	$("composer-send").disabled = true;

	$("transcript").append(bubble("user", text));
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
		const res = await api("/api/chats/" + current.id + "/send", {
			method: "POST",
			body: JSON.stringify({ text }),
		});
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
					if (payload.title) $("chat-title").textContent = payload.title;
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
			body.innerHTML = renderReply(beats[0].content);
			for (const b of beats.slice(1)) {
				$("transcript").append(bubble("assistant", b.content, b.who, b.accent));
			}
		} else {
			body.innerHTML = renderReply(reply);
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
