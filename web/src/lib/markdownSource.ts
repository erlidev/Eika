import { common, createLowlight } from "lowlight";

/**
 * What a run of markdown source is, for a box that shows the source as it is
 * typed: the syntax (`marker`), and what that syntax does to the text it
 * wraps. A run can be several at once, as code inside a heading is. `code`
 * is inline code; `block` is the content of a fenced block.
 */
export type SourceKind =
  | "marker"
  | "heading"
  | "strong"
  | "emphasis"
  | "strike"
  | "code"
  | "block"
  | "link"
  | "quote"
  | "bullet";

/**
 * SourceSpan is one run of the source and what it is. `token` is the
 * highlight.js classes of a run in a fenced block whose language is known,
 * the same classes the transcript's code blocks are coloured by.
 */
export type SourceSpan = { text: string; kinds: SourceKind[]; token?: string };

const fence = /^ {0,3}(`{3,}|~{3,})/;
const heading = /^( {0,3}#{1,6})([ \t].*|)$/;
const rule = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/;
const quote = /^ {0,3}>[ \t]?/;
const bullet = /^([ \t]*)([-*+]|\d{1,9}[.)])([ \t]+)(\[[ xX]\][ \t])?/;

/**
 * Inline syntax, tried where its first character appears. Every pattern is
 * sticky: it matches where it is tried or not at all.
 */
const inlineCode = /(`+)(.+?)\1(?!`)/y;
const link = /(!?\[)([^\]]*)(\]\([^)\s]*\))/y;
const strong = /(\*\*|__)(?=\S)(.+?)(?<=\S)\1/y;
const strike = /(~~)(?=\S)(.+?)(?<=\S)\1/y;
const emphasis = /(\*|_)(?=[^\s*_])(.+?)(?<=[^\s*_])\1(?![*_])/y;
const url = /https?:\/\/[^\s<>]*[^\s<>.,:;"')\]]/y;

const wordChar = /\w/;

/**
 * lowlight holds the grammars the transcript highlights with. A block is
 * highlighted only when its fence names a language: guessing one tries every
 * grammar, which costs tens of milliseconds a keystroke.
 */
const lowlight = createLowlight(common);

function span(text: string, kinds: SourceKind[], token?: string): SourceSpan {
  return token === undefined ? { text, kinds } : { text, kinds, token };
}

/** at runs a sticky pattern at `i`, its groups as strings, or null. */
function at(re: RegExp, text: string, i: number): string[] | null {
  re.lastIndex = i;
  const m = re.exec(text);
  return m === null ? null : Array.from(m, (g: string | undefined) => g ?? "");
}

/**
 * syntaxAt is the inline syntax that starts at `i`, as spans, or null when
 * none does. Code is tried first because its content is literal, and `**`
 * before `*`.
 */
function syntaxAt(text: string, i: number, kinds: SourceKind[]): SourceSpan[] | null {
  const c = text[i];
  const marker: SourceKind[] = [...kinds, "marker"];
  // `_` and a bare URL start only at a word's start: snake_case is a name.
  const wordStart = i === 0 || !wordChar.test(text[i - 1] ?? "");
  if (c === "`") {
    const [, ticks = "", body = ""] = at(inlineCode, text, i) ?? [];
    if (ticks !== "")
      return [span(ticks, marker), span(body, [...kinds, "code"]), span(ticks, marker)];
  }
  if (c === "[" || c === "!") {
    const [, open = "", label = "", target = ""] = at(link, text, i) ?? [];
    if (open !== "")
      return [span(open, marker), ...inline(label, [...kinds, "link"]), span(target, marker)];
  }
  if (c === "*" || (c === "_" && wordStart)) {
    const [whole = "", mark = "", body = ""] = at(strong, text, i) ?? at(emphasis, text, i) ?? [];
    const midWord = c === "_" && wordChar.test(text[i + whole.length] ?? "");
    if (mark !== "" && !midWord) {
      const kind = mark.length === 2 ? "strong" : "emphasis";
      return [span(mark, marker), ...inline(body, [...kinds, kind]), span(mark, marker)];
    }
  }
  if (c === "~") {
    const [, mark = "", body = ""] = at(strike, text, i) ?? [];
    if (mark !== "")
      return [span(mark, marker), ...inline(body, [...kinds, "strike"]), span(mark, marker)];
  }
  if (c === "h" && wordStart) {
    const [whole = ""] = at(url, text, i) ?? [];
    if (whole !== "") return [span(whole, [...kinds, "link"])];
  }
  return null;
}

/**
 * inline splits one line's text into its inline syntax. `kinds` is what the
 * whole text already is, so strong text inside a heading is both.
 */
function inline(text: string, kinds: SourceKind[]): SourceSpan[] {
  const spans: SourceSpan[] = [];
  let plain = 0;
  let i = 0;
  while (i < text.length) {
    const found = syntaxAt(text, i, kinds);
    if (found === null) {
      i++;
      continue;
    }
    if (i > plain) spans.push(span(text.slice(plain, i), kinds));
    spans.push(...found);
    i += found.reduce((n, s) => n + s.text.length, 0);
    plain = i;
  }
  if (plain < text.length) spans.push(span(text.slice(plain), kinds));
  return spans;
}

/** line splits one line outside a fence into its block and inline syntax. */
function line(text: string): SourceSpan[] {
  const h = heading.exec(text);
  if (h) return [span(h[1] ?? "", ["marker"]), ...inline(h[2] ?? "", ["heading"])];
  if (rule.test(text)) return [span(text, ["marker"])];
  const spans: SourceSpan[] = [];
  const kinds: SourceKind[] = [];
  let rest = text;
  const q = quote.exec(rest);
  if (q) {
    spans.push(span(q[0], ["marker"]));
    rest = rest.slice(q[0].length);
    kinds.push("quote");
  }
  const b = bullet.exec(rest);
  if (b) {
    spans.push(
      span(b[1] ?? "", kinds),
      span(b[2] ?? "", [...kinds, "bullet"]),
      span(b[3] ?? "", kinds),
      span(b[4] ?? "", [...kinds, "marker"]),
    );
    rest = rest.slice(b[0].length);
  }
  return [...spans, ...inline(rest, kinds)];
}

type HighlightNode = {
  type: string;
  value?: string;
  properties?: { className?: unknown };
  children?: HighlightNode[];
};

/** tokens flattens a highlighted tree into runs, each with the classes it sits in. */
function tokens(node: HighlightNode, classes: string[]): SourceSpan[] {
  if (node.type === "text") {
    const token = classes.join(" ");
    return [span(node.value ?? "", ["block"], token === "" ? undefined : token)];
  }
  const own = node.properties?.className;
  const inner = Array.isArray(own) ? [...classes, ...own.map(String)] : classes;
  return (node.children ?? []).flatMap((child) => tokens(child, inner));
}

/**
 * block is the content of a fenced block, highlighted when `info`, what
 * follows the opening fence, names a language lowlight knows.
 */
function block(content: string, info: string): SourceSpan[] {
  const language = info.trim().split(/\s/)[0] ?? "";
  if (language === "" || !lowlight.registered(language)) return [span(content, ["block"])];
  return tokens(lowlight.highlight(language, content), []);
}

/**
 * fenceOf is the fence a line opens or closes a block with, or "" when it
 * is not a fence line, and the info string after it.
 */
function fenceOf(text: string): [string, string] {
  const m = fence.exec(text);
  return m ? [m[1] ?? "", text.slice(m[0].length)] : ["", ""];
}

/** closes is whether a fence line ends the block `open` began. */
function closes(line: string, open: string): boolean {
  const [f, info] = fenceOf(line);
  return f.length >= open.length && f.startsWith(open.charAt(0)) && info.trim() === "";
}

/**
 * openFence is the fence of the block still open at the end of `source`, or
 * "" when every block in it is closed: whether a line is code or prose.
 */
export function openFence(source: string): string {
  let open = "";
  for (const text of source.split("\n")) {
    if (open !== "") {
      if (closes(text, open)) open = "";
    } else {
      open = fenceOf(text)[0];
    }
  }
  return open;
}

/** CodeBlock is where a fenced block's content lies in the source, its fences left out. */
export type CodeBlock = { start: number; end: number };

/**
 * codeBlocks is every fenced block's content, in order. A block still open
 * at the end of the source runs to its end.
 */
export function codeBlocks(source: string): CodeBlock[] {
  const blocks: CodeBlock[] = [];
  let open = "";
  let start = 0;
  let at = 0;
  for (const text of source.split("\n")) {
    const next = at + text.length + 1;
    if (open === "") {
      open = fenceOf(text)[0];
      start = Math.min(next, source.length);
    } else if (closes(text, open)) {
      open = "";
      blocks.push({ start, end: Math.max(start, at - 1) });
    }
    at = next;
  }
  if (open !== "") blocks.push({ start, end: source.length });
  return blocks;
}

/**
 * markdownSource splits markdown source into runs, for showing it with its
 * syntax marked while it is still being typed. The runs join back into the
 * source exactly, newlines included: a box that draws them behind its text
 * depends on every character staying where it was. It reads line by line,
 * as a person scans what they wrote, and is not a full parser: it has to be
 * cheap on every keystroke and forgiving of half-written syntax.
 */
export function markdownSource(source: string): SourceSpan[] {
  const spans: SourceSpan[] = [];
  const push = ({ text, kinds, token }: SourceSpan) => {
    if (text === "") return;
    const last = spans.at(-1);
    if (last?.kinds.join() === kinds.join() && last.token === token) last.text += text;
    else spans.push(span(text, kinds, token));
  };

  const lines = source.split("\n");
  let i = 0;
  while (i < lines.length) {
    const text = lines[i] ?? "";
    if (i > 0) push(span("\n", []));
    const [open, info] = fenceOf(text);
    i++;
    if (open === "") {
      line(text).forEach(push);
      continue;
    }
    // A fenced block is highlighted whole: a grammar's state, inside a
    // comment or a string, runs across lines.
    push(span(text, ["marker"]));
    const start = i;
    while (i < lines.length && !closes(lines[i] ?? "", open)) i++;
    if (i > start) {
      push(span("\n", []));
      block(lines.slice(start, i).join("\n"), info).forEach(push);
    }
    if (i < lines.length) {
      push(span("\n", []));
      push(span(lines[i] ?? "", ["marker"]));
      i++;
    }
  }
  return spans;
}
