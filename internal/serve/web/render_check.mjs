// Renders the shared corpus with the phone's renderer and prints the visible
// text of each case, one JSON string per line.
//
// Driven by the Go test next door, which renders the same corpus with the
// desktop's renderer and compares. Two renderers showing a reader different
// words is a bug whichever one is wrong, and only a comparison finds it.
import fs from "fs";
import path from "path";

const dir = path.dirname(new URL(import.meta.url).pathname);
const src = fs.readFileSync(path.join(dir, "app.js"), "utf8");
const from = src.indexOf("function escape(s)");
const to = src.indexOf("// ---- Screens ----");
if (from < 0 || to < 0) {
	console.error("could not find the renderer block in app.js");
	process.exit(1);
}
const R = new Function(src.slice(from, to) + "\nreturn {renderMarkup, replyLine, ownLine, plainLine};")();

const strip = (html) =>
	html
		.replace(/<[^>]*>/g, "")
		.replace(/&lt;/g, "<")
		.replace(/&gt;/g, ">")
		.replace(/&amp;/g, "&");

// styles reduces markup to one letter per visible character, naming what is
// styling it. Comparing only the words would miss the half of a rendering bug
// that shows the right words in the wrong style, which is most of what the
// reader notices: speech set as narration reads as narration.
const STYLE = { narration: "N", speech: "S", strong: "B", em: "I", tt: "C", code: "C" };

function styles(html) {
	let out = "", i = 0;
	const stack = [];
	while (i < html.length) {
		if (html[i] !== "<") {
			// One letter per character of text, entities counted as one.
			if (html[i] === "&") {
				const end = html.indexOf(";", i);
				i = end < 0 ? i + 1 : end + 1;
			} else {
				i++;
			}
			out += stack.length ? stack[stack.length - 1] : "-";
			continue;
		}
		const end = html.indexOf(">", i);
		const tag = html.slice(i + 1, end);
		i = end + 1;
		if (tag.startsWith("/")) {
			stack.pop();
			continue;
		}
		const name = tag.split(/[\s>]/)[0];
		const cls = /class="([^"]+)"/.exec(tag);
		stack.push(STYLE[cls ? cls[1] : name] || "?");
	}
	return out;
}

// The corpus to render: the committed cases by default, or a file named on the
// command line, which is how the Go test feeds it generated ones as well.
const corpus = process.argv[2] || path.join(dir, "rendercases.json");
const cases = JSON.parse(fs.readFileSync(corpus, "utf8"));
for (const c of cases) {
	const reply = R.renderMarkup(c, R.replyLine);
	const own = R.renderMarkup(c, R.ownLine);
	const plain = R.renderMarkup(c, R.plainLine);
	console.log(JSON.stringify({
		reply: strip(reply), own: strip(own), plain: strip(plain),
		replyStyles: styles(reply), ownStyles: styles(own), plainStyles: styles(plain),
	}));
}
