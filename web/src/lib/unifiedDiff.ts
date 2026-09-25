/**
 * Reads the text `git diff` and `git status --porcelain` print into the
 * shapes the UI draws: one FileDiff per file, its hunks as DiffHunks of
 * DiffLines, and one StatusEntry per status line. The
 * workspace diff route returns both texts as they are (docs/api/http.md).
 */

/** DiffLine is one rendered row of a unified diff. */
export type DiffLine = {
  kind: "context" | "add" | "remove";
  text: string;
  /** oldLine is the 1-based line number in the old text, absent for an addition. */
  oldLine?: number;
  /** newLine is the 1-based line number in the new text, absent for a removal. */
  newLine?: number;
  /** noNewline marks the last line of a text that does not end in a newline. */
  noNewline?: boolean;
};

/** DiffHunk is a run of changed lines with the context around it. */
export type DiffHunk = {
  oldStart: number;
  newStart: number;
  /** header is what git prints after a hunk's ranges, usually the enclosing function. */
  header?: string;
  lines: DiffLine[];
};

/** FileChange is what happened to a file. */
export type FileChange = "modified" | "added" | "deleted" | "renamed" | "copied";

/** FileDiff is one file's part of a unified diff. */
export type FileDiff = {
  /** path is where the file is now, or where it was when it was deleted. */
  path: string;
  /** oldPath is where it was before a rename or copy; otherwise equal to path. */
  oldPath: string;
  change: FileChange;
  /** binary says git printed no text diff for it. */
  binary: boolean;
  hunks: DiffHunk[];
  additions: number;
  deletions: number;
};

/** StatusEntry is one line of `git status --porcelain`. */
export type StatusEntry = {
  /** code is the two status letters, such as " M" or "??". */
  code: string;
  path: string;
  /** origPath is the old path of a rename. */
  origPath?: string;
};

const hunkHeader = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$/;

/** parseUnifiedDiff splits a unified diff into files. Text it does not understand is skipped. */
export function parseUnifiedDiff(text: string): FileDiff[] {
  const files: FileDiff[] = [];
  let file: FileDiff | undefined;
  let hunk: DiffHunk | undefined;
  let oldLeft = 0;
  let newLeft = 0;
  let oldLine = 0;
  let newLine = 0;

  const start = (path: string, oldPath: string): FileDiff => {
    const next: FileDiff = {
      path,
      oldPath,
      change: "modified",
      binary: false,
      hunks: [],
      additions: 0,
      deletions: 0,
    };
    files.push(next);
    file = next;
    hunk = undefined;
    oldLeft = 0;
    newLeft = 0;
    return next;
  };

  const lines = text.split("\n");
  // A diff ends with a newline, which is not an empty last line of the last hunk.
  if (lines.at(-1) === "") lines.pop();

  for (const line of lines) {
    if (hunk && file && (oldLeft > 0 || newLeft > 0)) {
      const row = hunkLine(line, oldLine, newLine);
      if (row) {
        hunk.lines.push(row);
        if (row.kind !== "add") {
          oldLeft -= 1;
          oldLine += 1;
        }
        if (row.kind !== "remove") {
          newLeft -= 1;
          newLine += 1;
        }
        if (row.kind === "add") file.additions += 1;
        if (row.kind === "remove") file.deletions += 1;
        continue;
      }
    }
    if (line.startsWith("\\")) {
      // "\ No newline at end of file" belongs to the line before it.
      const last = hunk?.lines.at(-1);
      if (last) last.noNewline = true;
      continue;
    }
    if (line.startsWith("diff --git ")) {
      const [oldPath, newPath] = gitHeaderPaths(line.slice("diff --git ".length));
      start(newPath, oldPath);
      continue;
    }
    const header = hunkHeader.exec(line);
    if (header && file) {
      oldLine = Number(header[1]);
      newLine = Number(header[3]);
      oldLeft = header[2] === undefined ? 1 : Number(header[2]);
      newLeft = header[4] === undefined ? 1 : Number(header[4]);
      hunk = { oldStart: oldLine, newStart: newLine, lines: [] };
      if (header[5] !== undefined && header[5] !== "") hunk.header = header[5];
      file.hunks.push(hunk);
      continue;
    }
    if (line.startsWith("--- ")) {
      const path = diffPath(line.slice(4));
      // A plain `diff -u` has no git header, so its "---" starts the file.
      const current = !file || file.hunks.length > 0 ? start(path ?? "", path ?? "") : file;
      if (path === undefined) current.change = "added";
      else current.oldPath = path;
      continue;
    }
    if (!file) continue;
    if (line.startsWith("+++ ")) {
      const path = diffPath(line.slice(4));
      if (path === undefined) file.change = "deleted";
      else file.path = path;
    } else if (line.startsWith("new file mode")) {
      file.change = "added";
    } else if (line.startsWith("deleted file mode")) {
      file.change = "deleted";
    } else if (line.startsWith("rename from ")) {
      file.change = "renamed";
      file.oldPath = unquote(line.slice("rename from ".length));
    } else if (line.startsWith("rename to ")) {
      file.change = "renamed";
      file.path = unquote(line.slice("rename to ".length));
    } else if (line.startsWith("copy from ")) {
      file.change = "copied";
      file.oldPath = unquote(line.slice("copy from ".length));
    } else if (line.startsWith("copy to ")) {
      file.change = "copied";
      file.path = unquote(line.slice("copy to ".length));
    } else if (line.startsWith("Binary files ") || line === "GIT binary patch") {
      file.binary = true;
    }
  }

  for (const f of files) {
    // A deleted file is named by where it was.
    if (f.change === "deleted" && f.path === "") f.path = f.oldPath;
    if (f.change === "added" && f.oldPath === "") f.oldPath = f.path;
  }
  return files;
}

/** hunkLine reads one line inside a hunk, or undefined when it is not one. */
function hunkLine(line: string, oldLine: number, newLine: number): DiffLine | undefined {
  const mark = line.charAt(0);
  const text = line.slice(1);
  if (mark === "+") return { kind: "add", text, newLine };
  if (mark === "-") return { kind: "remove", text, oldLine };
  // Some tools strip the space off an empty context line.
  if (mark === " " || line === "") return { kind: "context", text, oldLine, newLine };
  return undefined;
}

/** diffPath reads a "---" or "+++" path, undefined for /dev/null. */
function diffPath(raw: string): string | undefined {
  // git ends a path with a space-containing name in a tab.
  const path = unquote(raw.split("\t")[0] ?? "");
  if (path === "/dev/null") return undefined;
  return stripPrefix(path);
}

function stripPrefix(path: string): string {
  return path.startsWith("a/") || path.startsWith("b/") ? path.slice(2) : path;
}

/**
 * gitHeaderPaths reads "a/<old> b/<new>". A path with a space makes the split
 * ambiguous, so equal halves (the common case) are preferred; the "---",
 * "+++", and rename lines that follow correct the rest.
 */
function gitHeaderPaths(rest: string): [string, string] {
  if (rest.startsWith('"')) {
    const match = /^("(?:[^"\\]|\\.)*"|\S+) ("(?:[^"\\]|\\.)*"|\S+)$/.exec(rest);
    if (match) return [stripPrefix(unquote(match[1] ?? "")), stripPrefix(unquote(match[2] ?? ""))];
  }
  const half = (rest.length - 1) / 2;
  if (Number.isInteger(half)) {
    const before = rest.slice(0, half);
    const after = rest.slice(half + 1);
    if (before.slice(2) === after.slice(2)) return [stripPrefix(before), stripPrefix(after)];
  }
  const at = rest.indexOf(" b/");
  if (at < 0) return [stripPrefix(rest), stripPrefix(rest)];
  return [stripPrefix(rest.slice(0, at)), stripPrefix(rest.slice(at + 1))];
}

/**
 * unquote reads a path git wrote in C-style quotes because it holds special
 * or non-ASCII characters, whose bytes it writes as octal escapes.
 */
export function unquote(raw: string): string {
  if (!(raw.startsWith('"') && raw.endsWith('"') && raw.length >= 2)) return raw;
  const body = raw.slice(1, -1);
  const bytes: number[] = [];
  const escapes: Record<string, number> = {
    n: 10,
    t: 9,
    r: 13,
    '"': 34,
    "\\": 92,
    a: 7,
    b: 8,
    f: 12,
    v: 11,
  };
  for (let i = 0; i < body.length; i++) {
    const ch = body.charAt(i);
    if (ch !== "\\") {
      for (const byte of new TextEncoder().encode(ch)) bytes.push(byte);
      continue;
    }
    const next = body.charAt(i + 1);
    const octal = /^[0-7]{3}/.exec(body.slice(i + 1));
    if (octal) {
      bytes.push(Number.parseInt(octal[0], 8));
      i += 3;
    } else {
      bytes.push(escapes[next] ?? next.charCodeAt(0));
      i += 1;
    }
  }
  return new TextDecoder().decode(new Uint8Array(bytes));
}

/** parseStatus reads `git status --porcelain` (version 1) into entries. */
export function parseStatus(text: string): StatusEntry[] {
  const entries: StatusEntry[] = [];
  for (const line of text.split("\n")) {
    if (line.length < 4) continue;
    const code = line.slice(0, 2);
    const rest = line.slice(3);
    const arrow = code.includes("R") || code.includes("C") ? splitRename(rest) : undefined;
    entries.push(
      arrow
        ? { code, path: unquote(arrow[1]), origPath: unquote(arrow[0]) }
        : { code, path: unquote(rest) },
    );
  }
  return entries;
}

function splitRename(rest: string): [string, string] | undefined {
  const at = rest.indexOf(" -> ");
  return at < 0 ? undefined : [rest.slice(0, at), rest.slice(at + 4)];
}

/** StatusSummary counts the files a status lists. */
export type StatusSummary = {
  /** changed is every tracked file with a change, staged or not. */
  changed: number;
  untracked: number;
  /** conflicted is files git could not merge. */
  conflicted: number;
};

const conflictCodes = new Set(["DD", "AU", "UD", "UA", "DU", "AA", "UU"]);

/** summarizeStatus counts a status's entries by kind. */
export function summarizeStatus(entries: StatusEntry[]): StatusSummary {
  const summary: StatusSummary = { changed: 0, untracked: 0, conflicted: 0 };
  for (const entry of entries) {
    if (entry.code === "??") summary.untracked += 1;
    else if (conflictCodes.has(entry.code)) summary.conflicted += 1;
    else if (entry.code !== "!!") summary.changed += 1;
  }
  return summary;
}
