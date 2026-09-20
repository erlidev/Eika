/** The unified diff the `edit` tool's card shows. */

import { DiffRows } from "@/components/DiffRows";
import { diffLines } from "@/lib/diff";

export type EditDiffProps = {
  /** oldString is the text the edit replaced. */
  oldString: string;
  /** newString is the text that took its place. */
  newString: string;
  /** startLine is the file line the replacement began at, for the gutter. */
  startLine: number;
};

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
      <DiffRows
        lines={lines}
        lineNumber={(line) =>
          line.oldLine === undefined ? undefined : startLine + line.oldLine - 1
        }
      />
    </div>
  );
}
