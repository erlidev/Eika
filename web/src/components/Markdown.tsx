/**
 * Assistant prose. Markdown with GitHub tables and task lists, and syntax
 * highlighting applied to fenced code at render time.
 */

import ReactMarkdown from "react-markdown";
import type { Options } from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import remarkGfm from "remark-gfm";

import { cn } from "@/lib/utils";

export type MarkdownProps = {
  children: string;
  className?: string;
};

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
};

/** plugins are built once: they hold no per-render state. */
const remarkPlugins: Options["remarkPlugins"] = [remarkGfm];
const rehypePlugins: Options["rehypePlugins"] = [
  [rehypeHighlight, { detect: true, ignoreMissing: true }],
];

export function Markdown({ children, className }: MarkdownProps) {
  return (
    <div
      className={cn(
        "text-sm leading-relaxed break-words",
        "[&_p]:my-2 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0",
        "[&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-5",
        "[&_li]:my-0.5",
        "[&_h1]:mt-4 [&_h1]:mb-2 [&_h1]:text-base [&_h1]:font-semibold",
        "[&_h2]:mt-4 [&_h2]:mb-2 [&_h2]:text-sm [&_h2]:font-semibold",
        "[&_h3]:mt-3 [&_h3]:mb-1 [&_h3]:text-sm [&_h3]:font-semibold",
        "[&_a]:underline [&_a]:underline-offset-2",
        "[&_blockquote]:border-border [&_blockquote]:text-muted-foreground [&_blockquote]:my-2 [&_blockquote]:border-l-2 [&_blockquote]:pl-3",
        "[&_code]:bg-muted [&_code]:rounded-sm [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[0.85em]",
        "[&_pre]:bg-muted [&_pre]:my-2 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:p-3",
        "[&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_pre_code]:text-xs",
        "[&_table]:my-2 [&_table]:block [&_table]:w-max [&_table]:max-w-full [&_table]:overflow-x-auto [&_table]:text-xs",
        "[&_th]:border-border [&_th]:border [&_th]:px-2 [&_th]:py-1 [&_th]:text-left",
        "[&_td]:border-border [&_td]:border [&_td]:px-2 [&_td]:py-1",
        "[&_hr]:border-border [&_hr]:my-3",
        className,
      )}
    >
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
