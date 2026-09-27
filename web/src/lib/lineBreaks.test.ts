import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import { describe, expect, it } from "vitest";

import { remarkLineBreaks } from "@/lib/lineBreaks";

function html(markdown: string): string {
  return renderToStaticMarkup(
    createElement(ReactMarkdown, { remarkPlugins: [remarkLineBreaks] }, markdown),
  );
}

describe("remarkLineBreaks", () => {
  const cases = [
    { name: "one line is untouched", in: "hello", out: "<p>hello</p>" },
    { name: "a newline becomes a break", in: "one\ntwo", out: "<p>one<br/>\ntwo</p>" },
    {
      name: "a newline inside emphasis becomes a break",
      in: "**one\ntwo**",
      out: "<p><strong>one<br/>\ntwo</strong></p>",
    },
    {
      name: "a blank line still starts a paragraph",
      in: "one\n\ntwo",
      out: "<p>one</p>\n<p>two</p>",
    },
    {
      name: "a list item keeps its lines",
      in: "- one\n  two",
      out: "<ul>\n<li>one<br/>\ntwo</li>\n</ul>",
    },
    {
      name: "fenced code is left alone",
      in: "```\na\nb\n```",
      out: "<pre><code>a\nb\n</code></pre>",
    },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(html(c.in)).toBe(c.out);
    });
  }
});
