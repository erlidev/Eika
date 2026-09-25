/**
 * The rows of a unified diff: a line-number gutter, the +/- mark, and the
 * text, tinted by kind, as the Changes panel draws a workspace diff.
 */

import type { DiffLine } from "@/lib/unifiedDiff";
import { cn } from "@/lib/utils";

export type DiffRowsProps = {
  lines: DiffLine[];
  /** lineNumber is the number the gutter shows for a line, if any. */
  lineNumber: (line: DiffLine) => number | undefined;
};

const marks = { context: " ", add: "+", remove: "-" } as const;

export function DiffRows({ lines, lineNumber }: DiffRowsProps) {
  return lines.map((line, index) => (
    <div
      key={`${String(index)}:${line.kind}`}
      className={cn(
        "flex gap-2 whitespace-pre",
        line.kind === "add" && "bg-success/10 text-success",
        line.kind === "remove" && "bg-destructive/10 text-destructive",
      )}
    >
      <span className="text-muted-foreground w-10 shrink-0 pr-1 text-right tabular-nums select-none">
        {lineNumber(line) ?? ""}
      </span>
      <span className="w-3 shrink-0 select-none">{marks[line.kind]}</span>
      <span className="pr-2">
        {line.text === "" ? " " : line.text}
        {line.noNewline === true && (
          <span className="text-muted-foreground pl-2 font-sans select-none">
            (no newline at end of file)
          </span>
        )}
      </span>
    </div>
  ));
}
