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

const SCREENS = ["pair", "home", "chats", "cast", "chat"];

function show(name) {
	for (const id of SCREENS) $(id).hidden = id !== name;
	$("tabs").hidden = name === "pair";
	document.body.classList.toggle("in-chat", name === "chat");
	for (const tab of document.querySelectorAll(".tab")) {
		tab.classList.toggle("is-on", tab.dataset.screen === name);
	}
}

function showPairing() { show("pair"); $("pair-code").focus(); }

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

function initialOf(name) {
	return (name || "?").trim().charAt(0).toUpperCase() || "?";
}

// ---- Home ----

async function loadState() {
	const res = await api("/api/state");
	state = await res.json();

	$("home-greeting").textContent = state.persona ? "Welcome back, " + state.persona : "Astral";
	$("home-model").textContent = state.model || "no model";

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
		for (const m of current.messages || []) t.append(bubble(m.role, m.content));
		show("chat");
		scrollDown(false);
		$("composer-text").focus();
	} catch (e) {
		toast(e.message);
	}
}

function bubble(role, content) {
	const wrap = document.createElement("div");
	wrap.className = "msg" + (role === "user" ? " from-user" : "");
	if (role !== "user" && current?.who) {
		const who = document.createElement("div");
		who.className = "who";
		who.textContent = current.who;
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
					// Plain text while it streams: half an asterisk is not
					// markup, and re-rendering per token would flicker.
					body.textContent = reply;
					scrollDown();
				} else if (event === "done") {
					reply = payload.content;
					if (payload.title) $("chat-title").textContent = payload.title;
				} else if (event === "error") {
					throw new Error(payload.error);
				}
			}
		}
		body.classList.remove("dots");
		body.innerHTML = renderReply(reply);
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
	tab.addEventListener("click", () => { current = null; show(tab.dataset.screen); });
}

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
