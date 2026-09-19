// Installs a filter's bindings as read-only globals and returns the renderer
// that turns whatever the filter returned into text. Run once per filter, in
// a fresh interpreter, before the filter itself.
(function (global, text, sectionsJSON) {
	"use strict";

	var lines = text.split("\n");
	var sections = JSON.parse(sectionsJSON);

	// The heading in force at each line, so a grep hit can say where it is.
	var headingAt = [];
	for (var n = 0; n < lines.length; n++) headingAt.push("");
	sections.forEach(function (s) {
		for (var i = s.from; i <= Math.min(s.to, lines.length - 1); i++) headingAt[i] = s.heading;
	});

	// A fresh regular expression without g or y: lastIndex is shared state,
	// and test would skip every other matching line.
	function matcher(pattern) {
		if (typeof pattern === "string") return new RegExp(pattern);
		if (!(pattern instanceof RegExp)) throw new Error("grep(re, ctx?) needs a regular expression or a string.");
		return new RegExp(pattern.source, pattern.flags.replace(/[gy]/g, ""));
	}

	// Matching lines with ctx lines either side. Adjacent and overlapping
	// runs merge, so no line is paid for twice.
	function grep(pattern, ctx) {
		if (ctx === undefined) ctx = 2;
		var re = matcher(pattern);
		var ranges = [];
		for (var i = 0; i < lines.length; i++) {
			if (!re.test(lines[i])) continue;
			var from = Math.max(0, i - ctx);
			var to = Math.min(lines.length - 1, i + ctx);
			var last = ranges[ranges.length - 1];
			if (last && from <= last.to + 1) last.to = Math.max(last.to, to);
			else ranges.push({ from: from, to: to, hit: i });
		}
		return ranges.map(function (r) {
			return { heading: headingAt[r.hit], from: r.from, to: r.to, text: lines.slice(r.from, r.to + 1).join("\n") };
		});
	}

	// Fenced code blocks, rails included. Only a rail of the same character
	// and at least the same length closes a block, so a shorter run inside a
	// longer fence stays content.
	function fences() {
		var blocks = [];
		var open = null;
		lines.forEach(function (line) {
			var rail = /^\s*(`{3,}|~{3,})(.*)$/.exec(line);
			if (open) {
				if (rail && rail[1][0] === open.mark[0] && rail[1].length >= open.mark.length && !rail[2].trim()) {
					blocks.push({ lang: open.lang, text: [open.mark + open.lang].concat(open.body, [open.mark]).join("\n") });
					open = null;
				} else {
					open.body.push(line);
				}
				return;
			}
			if (rail) open = { mark: rail[1], lang: rail[2].trim(), body: [] };
		});
		// An unterminated fence still holds content worth returning.
		if (open) blocks.push({ lang: open.lang, text: [open.mark + open.lang].concat(open.body).join("\n") });
		return blocks;
	}

	function code(lang) {
		return fences()
			.filter(function (b) { return !lang || b.lang.toLowerCase() === String(lang).toLowerCase(); })
			.map(function (b) { return b.text; });
	}

	// Read-only, so a filter that assigns to a binding does not change what
	// the rest of it reads.
	var values = { text: text, lines: lines, sections: sections, grep: grep, code: code };
	Object.keys(values).forEach(function (name) {
		Object.defineProperty(global, name, { value: values[name], writable: false, enumerable: true, configurable: false });
	});

	function isSection(v) {
		return typeof v === "object" && v !== null && typeof v.text === "string" && typeof v.level === "number";
	}
	function isHit(v) {
		return typeof v === "object" && v !== null && typeof v.text === "string" && typeof v.from === "number";
	}
	function renderHit(h) {
		return (h.heading ? h.heading + " · " : "") + "lines[" + h.from + ".." + h.to + "]\n" + h.text;
	}
	function json(value) {
		var out;
		try {
			out = JSON.stringify(value, null, 2);
		} catch (e) {
			throw new Error("filter returned an object that cannot be serialised. Return text instead.");
		}
		return "```json\n" + out + "\n```";
	}

	// Duck-typed rather than branded, because useful filters build new
	// objects out of the bindings and a brand would not survive the spread.
	return function render(value) {
		if (typeof value === "string") return { text: value };
		if (value instanceof Promise || (value !== null && typeof value === "object" && typeof value.then === "function")) {
			throw new Error("filter returned a Promise. Filters run synchronously; return text instead.");
		}
		if (isSection(value)) return { text: value.text, sections: 1 };
		if (isHit(value)) return { text: renderHit(value) };
		if (Array.isArray(value)) {
			if (value.length === 0) return { text: "" };
			// Single-line strings are lines and join as they stood; multi-line
			// strings are blocks, and two run together would read as one.
			if (value.every(function (v) { return typeof v === "string"; })) {
				var blocks = value.some(function (v) { return v.indexOf("\n") >= 0; });
				return { text: value.join(blocks ? "\n\n" : "\n") };
			}
			if (value.every(isSection)) {
				var ordered = value.slice().sort(function (a, b) { return (a.index || 0) - (b.index || 0); });
				return { text: ordered.map(function (s) { return s.text; }).join("\n\n"), sections: ordered.length };
			}
			if (value.every(isHit)) {
				var hits = value.slice().sort(function (a, b) { return a.from - b.from; });
				return { text: hits.map(renderHit).join("\n\n") };
			}
			return { text: json(value) };
		}
		if (typeof value === "object" && value !== null) return { text: json(value) };
		throw new Error("filter returned " + (value === undefined ? "undefined" : String(value)) +
			". Return a string, a section, or an array of either.");
	};
})
