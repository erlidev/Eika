import { describe, expect, it } from "vitest";

import { markdownSource, openFence } from "@/lib/markdownSource";

/**
 * runs writes spans compactly: plain text bare, marked text as `kind:text`,
 * and a highlighted token as `kind(token):text`.
 */
function runs(source: string): string[] {
  return markdownSource(source).map((s) => {
    const token = s.token === undefined ? "" : `(${s.token})`;
    return s.kinds.length === 0 ? s.text : `${s.kinds.join("+")}${token}:${s.text}`;
  });
}

describe("markdownSource", () => {
  const cases: { name: string; in: string; out: string[] }[] = [
    { name: "plain text is one run", in: "just words", out: ["just words"] },
    {
      name: "strong text and its markers",
      in: "a **b** c",
      out: ["a ", "marker:**", "strong:b", "marker:**", " c"],
    },
    {
      name: "emphasis inside strong is both",
      in: "**a _b_**",
      out: [
        "marker:**",
        "strong:a ",
        "strong+marker:_",
        "strong+emphasis:b",
        "strong+marker:_",
        "marker:**",
      ],
    },
    {
      name: "snake_case is a name, not emphasis",
      in: "use max_retry_count here",
      out: ["use max_retry_count here"],
    },
    {
      name: "code is literal",
      in: "run `a **b**`",
      out: ["run ", "marker:`", "code:a **b**", "marker:`"],
    },
    {
      name: "a link's label, and its target as syntax",
      in: "see [the docs](https://x.dev) now",
      out: ["see ", "marker:[", "link:the docs", "marker:](https://x.dev)", " now"],
    },
    {
      name: "a bare URL, without the full stop after it",
      in: "at https://x.dev/a.",
      out: ["at ", "link:https://x.dev/a", "."],
    },
    {
      name: "a heading",
      in: "## Plan **now**",
      out: [
        "marker:##",
        "heading: Plan ",
        "heading+marker:**",
        "heading+strong:now",
        "heading+marker:**",
      ],
    },
    {
      name: "list items and a task box",
      in: "- one\n  2. [x] two",
      out: ["bullet:-", " one\n  ", "bullet:2.", " ", "marker:[x] ", "two"],
    },
    {
      name: "a quotation",
      in: "> said ~~so~~",
      out: ["marker:> ", "quote:said ", "quote+marker:~~", "quote+strike:so", "quote+marker:~~"],
    },
    { name: "a rule is not a list", in: "- - -", out: ["marker:- - -"] },
    {
      name: "a fenced block is literal until its closing fence",
      in: "```\nx := `**`\n```\n**b**",
      out: [
        "marker:```",
        "\n",
        "block:x := `**`",
        "\n",
        "marker:```",
        "\n",
        "marker:**",
        "strong:b",
        "marker:**",
      ],
    },
    {
      name: "a block in a named language is highlighted",
      in: "```go\nreturn 1 // done\n```",
      out: [
        "marker:```go",
        "\n",
        "block(hljs-keyword):return",
        "block: ",
        "block(hljs-number):1",
        "block: ",
        "block(hljs-comment):// done",
        "\n",
        "marker:```",
      ],
    },
    {
      name: "a comment is highlighted across lines",
      in: "```js\n/* a\nb */\n```",
      out: ["marker:```js", "\n", "block(hljs-comment):/* a\nb */", "\n", "marker:```"],
    },
    {
      name: "a block in an unknown language is plain",
      in: "```nosuchlang\nreturn\n```",
      out: ["marker:```nosuchlang", "\n", "block:return", "\n", "marker:```"],
    },
    {
      name: "an unclosed block runs to the end",
      in: "```go\nreturn",
      out: ["marker:```go", "\n", "block(hljs-keyword):return"],
    },
    {
      name: "a fence with text after it does not close a block",
      in: "```\n```go\n```",
      out: ["marker:```", "\n", "block:```go", "\n", "marker:```"],
    },
    {
      name: "a shorter fence does not close a longer one",
      in: "````\n```\n````",
      out: ["marker:````", "\n", "block:```", "\n", "marker:````"],
    },
    {
      name: "an unfinished marker is text",
      in: "a **b",
      out: ["a **b"],
    },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(runs(c.in)).toEqual(c.out);
    });
  }

  it("joins back into the source exactly", () => {
    const sources = [
      "",
      "\n\n",
      "# h\r\n- a\n\n> b `c`\n```\nd\n",
      "**unclosed _x `y [z](",
      "trailing newline\n",
      "```go\nfunc f() {\n\treturn\n}\n",
      "```\n\n```",
    ];
    for (const source of sources) {
      expect(
        markdownSource(source)
          .map((s) => s.text)
          .join(""),
      ).toBe(source);
    }
  });
});

describe("openFence", () => {
  const cases = [
    { name: "prose has no open fence", in: "a\n- b", out: "" },
    { name: "an open block", in: "a\n```go\nx", out: "```" },
    { name: "a closed block", in: "```\nx\n```\n", out: "" },
    { name: "a tilde fence", in: "~~~~\n```\n", out: "~~~~" },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(openFence(c.in)).toBe(c.out);
    });
  }
});
