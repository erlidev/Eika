/**
 * A line diff of two texts, as the rows DiffRows draws: the lines both
 * share as context, and the ones only one has as removals and additions.
 * It is for short texts a person edits, such as a prompt, so it compares
 * every line with every other (a longest common subsequence) rather than
 * taking the shortcuts git does for whole files.
 */

import type { DiffLine } from "@/lib/unifiedDiff";

/** maxCells bounds the comparison table; past it the texts are shown as replaced whole. */
const maxCells = 4_000_000;

/** lineDiff returns the rows that turn before into after. */
export function lineDiff(before: string, after: string): DiffLine[] {
  const a = before.split("\n");
  const b = after.split("\n");
  if (a.length * b.length > maxCells) return replaced(a, b);
  // common[i][j] is the length of the longest common run of a[i:] and b[j:].
  const common: number[][] = Array.from({ length: a.length + 1 }, () =>
    new Array<number>(b.length + 1).fill(0),
  );
  for (let i = a.length - 1; i >= 0; i--) {
    const row = common[i] ?? [];
    const below = common[i + 1] ?? [];
    for (let j = b.length - 1; j >= 0; j--) {
      row[j] = a[i] === b[j] ? (below[j + 1] ?? 0) + 1 : Math.max(below[j] ?? 0, row[j + 1] ?? 0);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    const left = a[i];
    const right = b[j];
    if (i < a.length && j < b.length && left === right) {
      out.push({ kind: "context", text: left ?? "", oldLine: i + 1, newLine: j + 1 });
      i++;
      j++;
    } else if (
      i < a.length &&
      (j >= b.length || (common[i + 1]?.[j] ?? 0) >= (common[i]?.[j + 1] ?? 0))
    ) {
      // On a tie the removal goes first, so a changed line reads old, then new.
      out.push({ kind: "remove", text: left ?? "", oldLine: i + 1 });
      i++;
    } else {
      out.push({ kind: "add", text: right ?? "", newLine: j + 1 });
      j++;
    }
  }
  return out;
}

/** replaced is every line of a removed and every line of b added. */
function replaced(a: string[], b: string[]): DiffLine[] {
  return [
    ...a.map((text, i): DiffLine => ({ kind: "remove", text, oldLine: i + 1 })),
    ...b.map((text, j): DiffLine => ({ kind: "add", text, newLine: j + 1 })),
  ];
}
