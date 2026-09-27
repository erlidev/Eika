/**
 * What a markdown editor does for the person typing into a plain textarea:
 * carry a list or a quotation on to the next line, nest a list item, indent
 * code, and close a code block as it is opened. Each is a pure function from the text
 * and the selection to the one edit to make, so the textarea can apply it as
 * typing (and undo it as typing) and the rules can be tested as data.
 */

import { codeBlocks, openFence } from "@/lib/markdownSource";
import type { CodeBlock } from "@/lib/markdownSource";

/**
 * TextEdit replaces `start` to `end` with `text`. The caret lands after the
 * text unless `selection` says what is selected afterwards.
 */
export type TextEdit = {
  start: number;
  end: number;
  text: string;
  selection?: [number, number];
};

/** prefix is a line's quotation markers, then its list marker, if any. */
const prefix =
  /^((?:[ \t]*>[ \t]?)*)([ \t]*)(?:([-*+])|(\d{1,9})([.)]))?([ \t]+)?(\[[ xX]\][ \t]+)?/;

type Prefix = {
  /** quote is the quotation markers, `> ` or `> > `, kept as typed. */
  quote: string;
  /** indent is the whitespace before a list marker. */
  indent: string;
  /** marker is `-`, `*`, `+`, or a number and its `.` or `)`; "" for none. */
  marker: string;
  number: number | null;
  delimiter: string;
  /** space is what separates the marker from the item's text. */
  space: string;
  task: boolean;
  /** length is how much of the line all of that takes. */
  length: number;
};

function parsePrefix(line: string): Prefix {
  const m = prefix.exec(line);
  const [
    ,
    quote = "",
    indent = "",
    bullet = "",
    digits = "",
    delimiter = "",
    space = "",
    task = "",
  ] = m ?? [];
  // A marker counts only with space after it: `-1` and `2.5` are text.
  const listed = (bullet !== "" || digits !== "") && space !== "";
  return {
    quote,
    indent: listed ? indent : "",
    marker: listed ? bullet || digits + delimiter : "",
    number: listed && digits !== "" ? Number(digits) : null,
    delimiter,
    space: listed ? space : "",
    task: listed && task !== "",
    length: listed ? (m?.[0].length ?? 0) : quote.length,
  };
}

function lineStart(text: string, at: number): number {
  return text.lastIndexOf("\n", at - 1) + 1;
}

function lineEnd(text: string, at: number): number {
  const end = text.indexOf("\n", at);
  return end === -1 ? text.length : end;
}

/**
 * newline is what Enter does. Inside a code block it keeps the line's
 * indentation. On a list item or in a quotation it starts the next item,
 * numbered one on, or the next quoted line; on an item or a quoted line with
 * nothing in it, it ends the list or the quotation instead, the way a second
 * Enter ends a paragraph.
 */
export function newline(text: string, start: number, end: number): TextEdit {
  const from = lineStart(text, start);
  const line = text.slice(from, lineEnd(text, start));
  if (openFence(text.slice(0, from)) !== "") {
    return { start, end, text: `\n${/^[ \t]*/.exec(line)?.[0] ?? ""}` };
  }
  const p = parsePrefix(line);
  // The caret inside the markers themselves splits the line as plain text.
  if (p.length === 0 || start - from < p.length) return { start, end, text: "\n" };
  if (line.slice(p.length).trim() === "" && start === end) {
    return { start: from, end: from + line.length, text: "" };
  }
  const marker = p.number === null ? p.marker : `${String(p.number + 1)}${p.delimiter}`;
  const task = p.task ? "[ ] " : "";
  return { start, end, text: `\n${p.quote}${p.indent}${marker}${p.space}${task}` };
}

/**
 * indent is what Tab does. In a code block it indents: at a caret it types
 * one step of indentation, and over a selection it indents every line; with
 * Shift it takes a step off each line. On a list item it nests the item
 * under the one above it, lining its marker up with that item's text, or
 * with Shift moves the item out to its parent's level. Anywhere else, and
 * for Shift on code with nothing left to take off, it is null, and Tab moves
 * focus as it does in any form: that is how the keyboard leaves the box.
 */
export function indent(text: string, start: number, end: number, out: boolean): TextEdit | null {
  const from = lineStart(text, start);
  // A selection that ends at the start of a line leaves that line out.
  const last = end > start && text[end - 1] === "\n" ? end - 1 : end;
  const to = lineEnd(text, last);
  const block = codeBlocks(text).find((b) => from >= b.start && to <= b.end);
  if (block) return indentCode(text, block, { start, end, from, to }, out);

  if (lineEnd(text, start) < end) return null;
  const p = parsePrefix(text.slice(from, lineEnd(text, start)));
  if (p.marker === "") return null;

  const width = p.indent.length;
  let target: number | null = out ? 0 : null;
  // Walk up through the items above to the one this item nests under, or,
  // moving out, to the parent whose level it takes.
  for (let at = from; at > 0;) {
    const above = lineStart(text, at - 1);
    const line = text.slice(above, at - 1);
    const q = parsePrefix(line);
    at = above;
    if (q.quote !== p.quote) break;
    if (q.marker === "") {
      // An item's wrapped text belongs to it; a blank line ends the list.
      if (line.slice(q.quote.length).trim() === "") break;
      continue;
    }
    const w = q.indent.length;
    if (!out && w === width) {
      target = width + q.marker.length + q.space.length;
      break;
    }
    if (out && w < width) {
      target = w;
      break;
    }
    if (!out && w < width) break;
  }
  if (target === null || target === width) return null;

  const at = from + p.quote.length;
  const shift = target - width;
  return {
    start: at,
    end: at + width,
    text: " ".repeat(target),
    selection: [Math.max(at + target, start + shift), Math.max(at + target, start + shift)],
  };
}

type Lines = {
  /** start and end are the selection. */
  start: number;
  end: number;
  /** from and to bound the whole lines it touches. */
  from: number;
  to: number;
};

/**
 * indentCode indents or outdents code by the block's own step: the
 * indentation of its first indented line, a tab or some spaces, or two
 * spaces for a block with none yet.
 */
function indentCode(text: string, block: CodeBlock, lines: Lines, out: boolean): TextEdit | null {
  const { start, end, from, to } = lines;
  const step = /^(\t| +)\S/m.exec(text.slice(block.start, block.end))?.[1] ?? "  ";
  if (!out && start === end) return { start, end, text: step };

  const spaces = step === "\t" ? 4 : step.length;
  let first = 0;
  let total = 0;
  const changed = text
    .slice(from, to)
    .split("\n")
    .map((line, i) => {
      let shift: number;
      let next: string;
      if (!out) {
        // A blank line stays empty rather than holding trailing indentation.
        shift = line === "" ? 0 : step.length;
        next = line === "" ? line : step + line;
      } else {
        const lead = line.startsWith("\t")
          ? 1
          : Math.min(spaces, /^ */.exec(line)?.[0].length ?? 0);
        shift = -lead;
        next = line.slice(lead);
      }
      if (i === 0) first = shift;
      total += shift;
      return next;
    });
  if (total === 0) return null;
  // A selection from the start of a line keeps the whole line selected.
  const head = start === from ? from : Math.max(from, start + first);
  const tail = start === end ? head : Math.max(head, end + total);
  return { start: from, end: to, text: changed.join("\n"), selection: [head, tail] };
}

/**
 * closeFence is what typing the third backtick of a fence does at the end of
 * a line: when that opens a block nothing closes yet, it adds the closing
 * fence below and leaves the caret where the language goes.
 */
export function closeFence(text: string, caret: number): TextEdit | null {
  const from = lineStart(text, caret);
  const opened = /^( {0,3})(`{3,})$/.exec(text.slice(from, caret));
  if (!opened || lineEnd(text, caret) !== caret) return null;
  if (openFence(text.slice(0, caret)) === "" || openFence(text) === "") return null;
  return {
    start: caret,
    end: caret,
    text: `\n${opened[1] ?? ""}${opened[2] ?? ""}`,
    selection: [caret, caret],
  };
}
