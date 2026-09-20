/**
 * Assistant prose. Markdown with GitHub tables and task lists, syntax
 * highlighting applied to fenced code at render time, and a typographic scale
 * of its own: a transcript is read for minutes at a time, so its text is
 * larger and its blocks are more strongly separated than the chrome around
 * it. Every colour is a theme token, so both schemes follow from `index.css`.
 */

import { Check, Copy } from "lucide-react";
import { Children, isValidElement, useState } from "react";
import ReactMarkdown from "react-markdown";
import type { Options } from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import remarkGfm from "remark-gfm";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export type MarkdownProps = {
  children: string;
  className?: string;
};

/** textOf flattens a rendered node back to the source text it was built from. */
function textOf(node: React.ReactNode): string {
  if (typeof node === "string") return node;
  if (typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(textOf).join("");
  if (isValidElement<{ children?: React.ReactNode }>(node)) return textOf(node.props.children);
  return "";
}

/**
 * languageOf reads the language `rehype-highlight` settled on. It marks the
 * code element with `language-<name>`, which is the only place the fence's
 * language survives to.
 */
function languageOf(node: React.ReactNode): string {
  for (const child of Children.toArray(node)) {
    if (!isValidElement<{ className?: string }>(child)) continue;
    const match = /language-(\w+)/.exec(child.props.className ?? "");
    if (match?.[1]) return match[1];
  }
  return "";
}

/**
 * CodeBlock is a fenced block: its language, a way to copy it, and the code.
 * The copy button is the reason this is a component rather than a class list:
 * code in a transcript is meant to be taken somewhere else.
 */
function CodeBlock({ children }: { children: React.ReactNode }) {
  const [copied, setCopied] = useState(false);
  const language = languageOf(children);
  const code = textOf(children);

  const copy = () => {
    void navigator.clipboard.writeText(code).then(
      () => {
        setCopied(true);
        setTimeout(() => {
          setCopied(false);
        }, 1500);
      },
      () => {
        // A browser that refuses the clipboard leaves the code selectable.
      },
    );
  };

  return (
    <div className="bg-muted/50 my-3 overflow-hidden rounded-md border">
      <div className="bg-muted/70 flex items-center gap-2 border-b px-2 py-1">
        <span className="text-muted-foreground font-mono text-2xs tracking-wide uppercase">
          {language === "" ? "code" : language}
        </span>
        <Button
          size="icon-xs"
          variant="ghost"
          className="ml-auto"
          aria-label={copied ? "Copied" : "Copy the code"}
          onClick={copy}
        >
          {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
        </Button>
      </div>
      <pre className="overflow-x-auto p-3 text-xs leading-relaxed">{children}</pre>
    </div>
  );
}

/**
 * components sends a link somewhere it cannot reach the app. Model output is
 * untrusted: `noopener` denies it `window.opener`, and `nofollow` keeps a
 * transcript from endorsing whatever it quoted.
 */
const components: Options["components"] = {
  a: ({ children, ...props }) => (
    <a {...props} target="_blank" rel="noopener noreferrer nofollow">
      {children}
    </a>
  ),
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
};

/** plugins are built once: they hold no per-render state. */
const remarkPlugins: Options["remarkPlugins"] = [remarkGfm];
const rehypePlugins: Options["rehypePlugins"] = [
  [rehypeHighlight, { detect: true, ignoreMissing: true }],
];

/**
 * prose is the transcript's typography. It is one list rather than a stack of
 * wrappers so that a delta re-renders one element tree, and it is grouped the
 * way it reads: blocks, headings, lists, emphasis, code, tables, rules.
 */
const prose = [
  "text-base break-words",
  // Blocks are separated by space, not by rules: a paragraph, a list, and a
  // quotation are the same voice at different volumes.
  "[&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
  "[&_p]:my-3",
  // A heading belongs to what follows it, so it keeps more space above than
  // below, and the top two carry a rule to break a long answer into parts.
  "[&_h1]:mt-6 [&_h1]:mb-3 [&_h1]:border-b [&_h1]:pb-1.5 [&_h1]:text-lg [&_h1]:font-semibold [&_h1]:tracking-tight",
  "[&_h2]:mt-6 [&_h2]:mb-2.5 [&_h2]:border-b [&_h2]:pb-1 [&_h2]:text-base [&_h2]:font-semibold [&_h2]:tracking-tight",
  "[&_h3]:mt-5 [&_h3]:mb-2 [&_h3]:text-base [&_h3]:font-semibold",
  "[&_h4]:mt-4 [&_h4]:mb-1.5 [&_h4]:text-sm [&_h4]:font-semibold [&_h4]:tracking-wide [&_h4]:uppercase [&_h4]:text-muted-foreground",
  // Markers are tinted so the shape of a list is visible before it is read.
  "[&_ul]:my-3 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-3 [&_ol]:list-decimal [&_ol]:pl-5",
  "[&_li]:my-1 [&_li]:pl-1 [&_li::marker]:text-primary [&_li::marker]:font-medium",
  "[&_li>ul]:my-1 [&_li>ol]:my-1",
  "[&_li_input]:mr-1.5 [&_li_input]:align-middle [&_li]:has-[input]:list-none [&_li]:has-[input]:-ml-4",
  "[&_strong]:font-semibold [&_strong]:text-foreground [&_em]:italic",
  "[&_a]:text-primary [&_a]:underline [&_a]:underline-offset-2 [&_a:hover]:decoration-2",
  // A quotation is set off by an accent bar rather than by a box, so quoted
  // prose still reads as prose.
  "[&_blockquote]:border-primary/40 [&_blockquote]:bg-muted/40 [&_blockquote]:text-muted-foreground [&_blockquote]:my-3 [&_blockquote]:rounded-r-md [&_blockquote]:border-l-2 [&_blockquote]:py-1.5 [&_blockquote]:pr-3 [&_blockquote]:pl-3",
  // Inline code is a chip; a fenced block is `CodeBlock`, which styles its own.
  "[&_code]:bg-muted [&_code]:border [&_code]:rounded-md [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-sm",
  "[&_pre_code]:border-0 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_pre_code]:text-xs",
  "[&_table]:my-3 [&_table]:block [&_table]:w-max [&_table]:max-w-full [&_table]:overflow-x-auto [&_table]:text-sm",
  "[&_thead]:bg-muted [&_th]:border-border [&_th]:border [&_th]:px-2.5 [&_th]:py-1.5 [&_th]:text-left [&_th]:font-semibold",
  "[&_td]:border-border [&_td]:border [&_td]:px-2.5 [&_td]:py-1.5 [&_td]:align-top",
  "[&_tbody_tr:nth-child(even)]:bg-muted/30",
  "[&_hr]:border-border [&_hr]:my-5",
  "[&_img]:my-3 [&_img]:max-w-full [&_img]:rounded-md [&_img]:border",
].join(" ");

export function Markdown({ children, className }: MarkdownProps) {
  return (
    <div className={cn(prose, className)}>
      <ReactMarkdown
        remarkPlugins={remarkPlugins}
        rehypePlugins={rehypePlugins}
        components={components}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
}
