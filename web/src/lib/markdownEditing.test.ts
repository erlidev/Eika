import { describe, expect, it } from "vitest";

import { closeFence, indent, newline } from "@/lib/markdownEditing";
import type { TextEdit } from "@/lib/markdownEditing";

/**
 * The cases write a selection into the text: `|` is a collapsed caret, and
 * `[` and `]` bound a selection. apply returns the text after the edit the
 * same way, so a case reads as before and after.
 */
function parse(marked: string): { text: string; start: number; end: number } {
  const caret = marked.indexOf("|");
  if (caret !== -1) return { text: marked.replace("|", ""), start: caret, end: caret };
  const start = marked.indexOf("[");
  const end = marked.indexOf("]") - 1;
  return { text: marked.replace("[", "").replace("]", ""), start, end };
}

function apply(text: string, edit: TextEdit | null): string | null {
  if (edit === null) return null;
  const after = text.slice(0, edit.start) + edit.text + text.slice(edit.end);
  const caret = edit.start + edit.text.length;
  const [head, tail] = edit.selection ?? [caret, caret];
  if (head === tail) return `${after.slice(0, head)}|${after.slice(head)}`;
  return `${after.slice(0, head)}[${after.slice(head, tail)}]${after.slice(tail)}`;
}

describe("newline", () => {
  const cases = [
    { name: "plain text breaks the line", in: "hello|", out: "hello\n|" },
    { name: "a bullet starts the next bullet", in: "- one|", out: "- one\n- |" },
    { name: "the bullet character is kept", in: "* one|", out: "* one\n* |" },
    { name: "a number counts on", in: "9. nine|", out: "9. nine\n10. |" },
    { name: "a parenthesis number keeps its delimiter", in: "1) one|", out: "1) one\n2) |" },
    { name: "a nested item keeps its indent", in: "- a\n  - b|", out: "- a\n  - b\n  - |" },
    { name: "a task starts an open task", in: "- [x] done|", out: "- [x] done\n- [ ] |" },
    { name: "a quotation goes on", in: "> said|", out: "> said\n> |" },
    { name: "a list in a quotation goes on", in: "> - a|", out: "> - a\n> - |" },
    { name: "an empty item ends the list", in: "- a\n- |", out: "- a\n|" },
    { name: "an empty quoted line ends the quotation", in: "> a\n> |", out: "> a\n|" },
    {
      name: "the caret mid-item splits it into two items",
      in: "- one| two",
      out: "- one\n- | two",
    },
    { name: "the caret inside the marker is plain", in: "|- one", out: "\n|- one" },
    { name: "a selection is replaced", in: "- o[ne]", out: "- o\n- |" },
    { name: "a dash with no space is text", in: "-1|", out: "-1\n|" },
    { name: "emphasis is not a bullet", in: "*a*|", out: "*a*\n|" },
    {
      name: "code keeps its indentation",
      in: "```go\nfunc f() {\n\tx := 1|",
      out: "```go\nfunc f() {\n\tx := 1\n\t|",
    },
    {
      name: "a bullet inside code is code",
      in: "```\n- a|",
      out: "```\n- a\n|",
    },
    {
      name: "a list after a closed block is a list",
      in: "```\nx\n```\n- a|",
      out: "```\nx\n```\n- a\n- |",
    },
  ];
  for (const c of cases) {
    it(c.name, () => {
      const { text, start, end } = parse(c.in);
      expect(apply(text, newline(text, start, end))).toBe(c.out);
    });
  }
});

describe("indent", () => {
  const cases = [
    { name: "an item nests under the one above", in: "- a\n- b|", out: "- a\n  - b|" },
    {
      name: "an item under a number lines up with its text",
      in: "10. a\n- b|",
      out: "10. a\n    - b|",
    },
    {
      name: "an item nests under the sibling, past its children",
      in: "- a\n  - x\n- b|",
      out: "- a\n  - x\n  - b|",
    },
    { name: "the first item cannot nest", in: "- a|", out: null },
    {
      name: "the first child cannot nest further",
      in: "- a\n  - b|",
      out: null,
    },
    { name: "a line that is not an item is left to Tab", in: "text|", out: null },
    {
      name: "wrapped text above the item is passed over",
      in: "- a\n  more\n- b|",
      out: "- a\n  more\n  - b|",
    },
    { name: "a blank line ends the list", in: "- a\n\n- b|", out: null },
    { name: "a selection over two lines is left to Tab", in: "- [a\n- b]", out: null },
    { name: "the caret keeps its place in the text", in: "- a\n- b|c", out: "- a\n  - b|c" },
    {
      name: "a quoted item nests inside the quotation",
      in: "> - a\n> - b|",
      out: "> - a\n>   - b|",
    },
  ];
  for (const c of cases) {
    it(c.name, () => {
      const { text, start, end } = parse(c.in);
      expect(apply(text, indent(text, start, end, false))).toBe(c.out);
    });
  }

  const outdents = [
    { name: "a nested item moves to its parent's level", in: "- a\n  - b|", out: "- a\n- b|" },
    {
      name: "a deeper item moves up one level",
      in: "- a\n  - b\n    - c|",
      out: "- a\n  - b\n  - c|",
    },
    { name: "a top item stays", in: "- a|", out: null },
  ];
  for (const c of outdents) {
    it(c.name, () => {
      const { text, start, end } = parse(c.in);
      expect(apply(text, indent(text, start, end, true))).toBe(c.out);
    });
  }
});

describe("indent in code", () => {
  const cases = [
    { name: "a caret types a step", in: "```\nx|\n```", out: "```\nx  |\n```" },
    {
      name: "the step is the block's own tab",
      in: "```go\nif x {\n\ty\n|\n```",
      out: "```go\nif x {\n\ty\n\t|\n```",
    },
    {
      name: "the step is the block's own spaces",
      in: "```py\ndef f():\n    x\n|",
      out: "```py\ndef f():\n    x\n    |",
    },
    {
      name: "a selection indents each line",
      in: "```\n[a\nb]\n```",
      out: "```\n[  a\n  b]\n```",
    },
    {
      name: "a selection within a line indents the line",
      in: "```\nab[c]d\n```",
      out: "```\n  ab[c]d\n```",
    },
    {
      name: "a blank line in a selection stays empty",
      in: "```\n[a\n\nb]\n```",
      out: "```\n[  a\n\n  b]\n```",
    },
    {
      name: "a selection ending at a line's start leaves that line out",
      in: "```\n[a\n]b\n```",
      out: "```\n[  a\n]b\n```",
    },
    { name: "a selection over a fence is left to Tab", in: "```\n[a\n```]", out: null },
    { name: "the opening fence is not code", in: "```go|\nx\n```", out: null },
    {
      name: "an empty line in a block is code",
      in: "```\n|\n```",
      out: "```\n  |\n```",
    },
    {
      name: "a list item in code is code",
      in: "```\n- a\n- b|\n```",
      out: "```\n- a\n- b  |\n```",
    },
  ];
  for (const c of cases) {
    it(c.name, () => {
      const { text, start, end } = parse(c.in);
      expect(apply(text, indent(text, start, end, false))).toBe(c.out);
    });
  }

  const outdents = [
    {
      name: "a step comes off the caret's line",
      in: "```\n  a\n    b|\n```",
      out: "```\n  a\n  b|\n```",
    },
    { name: "a tab comes off", in: "```go\n\t\ta|\n```", out: "```go\n\ta|\n```" },
    {
      name: "each selected line loses what it has, up to a step",
      in: "```\n  [  a\n b]\n```",
      out: "```\n[a\nb]\n```",
    },
    { name: "nothing to take off is left to Shift+Tab", in: "```\na|\n```", out: null },
  ];
  for (const c of outdents) {
    it(c.name, () => {
      const { text, start, end } = parse(c.in);
      expect(apply(text, indent(text, start, end, true))).toBe(c.out);
    });
  }
});

describe("closeFence", () => {
  const cases = [
    { name: "an opening fence is closed", in: "```|", out: "```|\n```" },
    { name: "a longer fence is closed with its length", in: "````|", out: "````|\n````" },
    { name: "an indented fence keeps its indent", in: "  ```|", out: "  ```|\n  ```" },
    { name: "a closing fence adds nothing", in: "```\nx\n```|", out: null },
    { name: "a fence already closed below adds nothing", in: "```|\nx\n```", out: null },
    { name: "backticks inside a line add nothing", in: "a ```|", out: null },
    { name: "the caret before the end of the line adds nothing", in: "```|go", out: null },
  ];
  for (const c of cases) {
    it(c.name, () => {
      const { text, start } = parse(c.in);
      expect(apply(text, closeFence(text, start))).toBe(c.out);
    });
  }
});
