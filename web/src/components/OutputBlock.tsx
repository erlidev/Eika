/**
 * A monospace block for tool output: command output, file contents, JSON. It
 * scrolls on both axes rather than wrapping, because alignment is what makes
 * command output readable.
 */

import { cn } from "@/lib/utils";

export type OutputBlockProps = {
  children: string;
  /** tone paints a failed call's output differently. */
  tone?: "default" | "error";
  /** maxHeightClass bounds a long output; the block scrolls inside it. */
  maxHeightClass?: string;
  /** label names the region for a screen reader. */
  label?: string;
  className?: string;
};

export function OutputBlock({
  children,
  tone = "default",
  maxHeightClass = "max-h-80",
  label,
  className,
}: OutputBlockProps) {
  return (
    <pre
      // A scrollable region needs to be focusable to be reachable by keyboard.
      tabIndex={0}
      role="group"
      aria-label={label}
      className={cn(
        "bg-muted/60 text-foreground overflow-auto rounded-md border p-2 font-mono text-xs leading-relaxed whitespace-pre",
        "focus-visible:ring-ring focus-visible:ring-1 focus-visible:outline-none",
        tone === "error" && "border-destructive/40 text-destructive",
        maxHeightClass,
        className,
      )}
    >
      {children}
    </pre>
  );
}
