/** The unified diff the `edit` tool's card shows. */

import { diffLines } from "@/lib/diff";
import { cn } from "@/lib/utils";

export type EditDiffProps = {
  /** oldString is the text the edit replaced. */
  oldString: string;
  /** newString is the text that took its place. */
  newString: string;
  /** startLine is the file line the replacement began at, for the gutter. */
  startLine: number;
};

const marks = { context: " ", add: "+", remove: "-" } as const;

/**
 * EditDiff renders old against new. The edit tool sends only the replaced
 * fragment, so this is the whole diff, not a hunk of a larger one.
 */
export function EditDiff({ oldString, newString, startLine }: EditDiffProps) {
  const lines = diffLines(oldString, newString);
  if (lines.length === 0) {
    return <p className="text-muted-foreground font-mono text-xs">no change</p>;
  }
  return (
    <div
      tabIndex={0}
      role="group"
      aria-label="unified diff"
      className="focus-visible:ring-ring max-h-96 overflow-auto rounded-md border font-mono text-xs leading-relaxed focus-visible:ring-1 focus-visible:outline-none"
    >
      {lines.map((line, index) => (
        <div
          key={`${String(index)}:${line.kind}`}
          className={cn(
            "flex gap-2 whitespace-pre",
            line.kind === "add" && "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
            line.kind === "remove" && "bg-rose-500/10 text-rose-700 dark:text-rose-400",
          )}
        >
          <span className="text-muted-foreground w-10 shrink-0 pr-1 text-right tabular-nums select-none">
            {line.oldLine === undefined ? "" : startLine + line.oldLine - 1}
          </span>
          <span className="w-3 shrink-0 select-none">{marks[line.kind]}</span>
          <span className="pr-2">{line.text === "" ? " " : line.text}</span>
        </div>
      ))}
    </div>
  );
}
