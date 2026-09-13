/**
 * A line diff for the `edit` tool renderer, which is the only place the UI
 * has two versions of a text and no diff from the harness. It is a plain
 * longest-common-subsequence over lines; nothing here needs to scale past the
 * size of a file the edit tool will rewrite.
 */

/** DiffLine is one rendered row of a unified diff. */
export type DiffLine = {
  kind: "context" | "add" | "remove";
  text: string;
  /** oldLine is the 1-based line number in the old text, absent for an addition. */
  oldLine?: number;
  /** newLine is the 1-based line number in the new text, absent for a removal. */
  newLine?: number;
};

/** DiffHunk is a run of changed lines with the context around it. */
export type DiffHunk = {
  oldStart: number;
  newStart: number;
  lines: DiffLine[];
};

/** maxDiffLines bounds the quadratic table; a longer text is shown whole. */
const maxDiffLines = 1500;

function splitLines(text: string): string[] {
  if (text === "") return [];
  const lines = text.split("\n");
  // A trailing newline ends the last line rather than starting an empty one.
  if (lines.at(-1) === "") lines.pop();
  return lines;
}

/**
 * diffLines compares two texts line by line. Every line of both texts appears
 * exactly once in the result, in order.
 */
export function diffLines(oldText: string, newText: string): DiffLine[] {
  const before = splitLines(oldText);
  const after = splitLines(newText);
  if (before.length > maxDiffLines || after.length > maxDiffLines) {
    return [
      ...before.map((text, i) => ({ kind: "remove" as const, text, oldLine: i + 1 })),
      ...after.map((text, i) => ({ kind: "add" as const, text, newLine: i + 1 })),
    ];
  }

  // table[i][j] is the length of the longest common subsequence of the
  // suffixes before[i..] and after[j..].
  const table: number[][] = Array.from({ length: before.length + 1 }, () =>
    new Array<number>(after.length + 1).fill(0),
  );
  for (let i = before.length - 1; i >= 0; i--) {
    for (let j = after.length - 1; j >= 0; j--) {
      const row = table[i];
      const nextRow = table[i + 1];
      if (!row || !nextRow) continue;
      row[j] =
        before[i] === after[j]
          ? (nextRow[j + 1] ?? 0) + 1
          : Math.max(nextRow[j] ?? 0, row[j + 1] ?? 0);
    }
  }

  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < before.length && j < after.length) {
    if (before[i] === after[j]) {
      out.push({ kind: "context", text: before[i] ?? "", oldLine: i + 1, newLine: j + 1 });
      i += 1;
      j += 1;
      continue;
    }
    const down = table[i + 1]?.[j] ?? 0;
    const right = table[i]?.[j + 1] ?? 0;
    if (down >= right) {
      out.push({ kind: "remove", text: before[i] ?? "", oldLine: i + 1 });
      i += 1;
    } else {
      out.push({ kind: "add", text: after[j] ?? "", newLine: j + 1 });
      j += 1;
    }
  }
  for (; i < before.length; i++)
    out.push({ kind: "remove", text: before[i] ?? "", oldLine: i + 1 });
  for (; j < after.length; j++) out.push({ kind: "add", text: after[j] ?? "", newLine: j + 1 });
  return out;
}

/**
 * diffHunks groups a diff into the changed regions with `context` unchanged
 * lines around each, which is what a unified diff shows.
 */
export function diffHunks(oldText: string, newText: string, context = 3): DiffHunk[] {
  const lines = diffLines(oldText, newText);
  const changed = lines
    .map((line, index) => (line.kind === "context" ? -1 : index))
    .filter((index) => index >= 0);
  if (changed.length === 0) return [];

  const hunks: DiffHunk[] = [];
  let start = Math.max(0, (changed[0] ?? 0) - context);
  let end = Math.min(lines.length, (changed[0] ?? 0) + context + 1);
  for (const index of changed.slice(1)) {
    if (index - context <= end) {
      end = Math.min(lines.length, index + context + 1);
      continue;
    }
    hunks.push(hunkOf(lines, start, end));
    start = Math.max(0, index - context);
    end = Math.min(lines.length, index + context + 1);
  }
  hunks.push(hunkOf(lines, start, end));
  return hunks;
}

function hunkOf(lines: DiffLine[], start: number, end: number): DiffHunk {
  const slice = lines.slice(start, end);
  const first = slice[0];
  return {
    oldStart: first?.oldLine ?? 1,
    newStart: first?.newLine ?? 1,
    lines: slice,
  };
}
