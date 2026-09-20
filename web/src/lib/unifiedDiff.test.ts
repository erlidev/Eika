import { describe, expect, it } from "vitest";

import { parseStatus, parseUnifiedDiff, summarizeStatus, unquote } from "@/lib/unifiedDiff";

const lines = (...rows: string[]) => rows.join("\n") + "\n";

describe("parseUnifiedDiff", () => {
  it("reads a modified file's hunks with line numbers", () => {
    const [file, ...rest] = parseUnifiedDiff(
      lines(
        "diff --git a/internal/client.go b/internal/client.go",
        "index 1111111..2222222 100644",
        "--- a/internal/client.go",
        "+++ b/internal/client.go",
        "@@ -48,3 +48,3 @@ func (c *Client) Send() error {",
        " \tif err == nil {",
        "-\t\t// retry immediately",
        "+\t\ttime.Sleep(backoff(attempt))",
        " \t}",
        "",
      ),
    );
    expect(rest).toEqual([]);
    expect(file).toMatchObject({
      path: "internal/client.go",
      oldPath: "internal/client.go",
      change: "modified",
      binary: false,
      additions: 1,
      deletions: 1,
    });
    expect(file?.hunks).toEqual([
      {
        oldStart: 48,
        newStart: 48,
        header: "func (c *Client) Send() error {",
        lines: [
          { kind: "context", text: "\tif err == nil {", oldLine: 48, newLine: 48 },
          { kind: "remove", text: "\t\t// retry immediately", oldLine: 49 },
          { kind: "add", text: "\t\ttime.Sleep(backoff(attempt))", newLine: 49 },
          { kind: "context", text: "\t}", oldLine: 50, newLine: 50 },
        ],
      },
    ]);
  });

  it("reads new and deleted files", () => {
    const files = parseUnifiedDiff(
      lines(
        "diff --git a/new.txt b/new.txt",
        "new file mode 100644",
        "index 0000000..e69de29",
        "--- /dev/null",
        "+++ b/new.txt",
        "@@ -0,0 +1,2 @@",
        "+one",
        "+two",
        "diff --git a/old.txt b/old.txt",
        "deleted file mode 100644",
        "index e69de29..0000000",
        "--- a/old.txt",
        "+++ /dev/null",
        "@@ -1 +0,0 @@",
        "-gone",
      ),
    );
    expect(files.map((f) => [f.path, f.oldPath, f.change, f.additions, f.deletions])).toEqual([
      ["new.txt", "new.txt", "added", 2, 0],
      ["old.txt", "old.txt", "deleted", 0, 1],
    ]);
    expect(files[1]?.hunks[0]?.lines).toEqual([{ kind: "remove", text: "gone", oldLine: 1 }]);
  });

  it("reads a pure rename and a rename with changes", () => {
    const files = parseUnifiedDiff(
      lines(
        "diff --git a/a.go b/b.go",
        "similarity index 100%",
        "rename from a.go",
        "rename to b.go",
        "diff --git a/c.go b/d.go",
        "similarity index 90%",
        "rename from c.go",
        "rename to d.go",
        "index 1..2 100644",
        "--- a/c.go",
        "+++ b/d.go",
        "@@ -1,1 +1,1 @@",
        "-package c",
        "+package d",
      ),
    );
    expect(files.map((f) => [f.oldPath, f.path, f.change, f.hunks.length])).toEqual([
      ["a.go", "b.go", "renamed", 0],
      ["c.go", "d.go", "renamed", 1],
    ]);
  });

  it("marks a line that ends without a newline", () => {
    const [file] = parseUnifiedDiff(
      lines(
        "diff --git a/x b/x",
        "--- a/x",
        "+++ b/x",
        "@@ -1 +1 @@",
        "-old",
        "\\ No newline at end of file",
        "+new",
        "\\ No newline at end of file",
      ),
    );
    expect(file?.hunks[0]?.lines).toEqual([
      { kind: "remove", text: "old", oldLine: 1, noNewline: true },
      { kind: "add", text: "new", newLine: 1, noNewline: true },
    ]);
  });

  it("reads a binary notice as a file with no hunks", () => {
    const files = parseUnifiedDiff(
      lines(
        "diff --git a/logo.png b/logo.png",
        "index 1..2 100644",
        "Binary files a/logo.png and b/logo.png differ",
        "diff --git a/data.bin b/data.bin",
        "new file mode 100644",
        "GIT binary patch",
        "literal 3",
        "KcmZ?wU;qFB0RR91",
        "",
        "literal 0",
        "HcmV?d00001",
      ),
    );
    expect(files.map((f) => [f.path, f.change, f.binary, f.hunks.length])).toEqual([
      ["logo.png", "modified", true, 0],
      ["data.bin", "added", true, 0],
    ]);
  });

  it("keeps removed lines that look like file headers", () => {
    const [file, ...rest] = parseUnifiedDiff(
      lines(
        "diff --git a/notes.md b/notes.md",
        "--- a/notes.md",
        "+++ b/notes.md",
        "@@ -1,2 +1,1 @@",
        "--- a heading rule",
        " kept",
      ),
    );
    expect(rest).toEqual([]);
    expect(file?.hunks[0]?.lines[0]).toEqual({
      kind: "remove",
      text: "-- a heading rule",
      oldLine: 1,
    });
  });

  it("reads paths with spaces and quoted non-ASCII paths", () => {
    const files = parseUnifiedDiff(
      lines(
        "diff --git a/my file.txt b/my file.txt",
        "--- a/my file.txt",
        "+++ b/my file.txt",
        "@@ -1 +1 @@",
        "-a",
        "+b",
        'diff --git "a/caf\\303\\251.txt" "b/caf\\303\\251.txt"',
        "new file mode 100644",
        "--- /dev/null",
        '+++ "b/caf\\303\\251.txt"',
        "@@ -0,0 +1 @@",
        "+x",
      ),
    );
    expect(files.map((f) => f.path)).toEqual(["my file.txt", "café.txt"]);
  });

  it("reads a plain diff -u with no git headers", () => {
    const files = parseUnifiedDiff(
      lines(
        "--- a/one",
        "+++ b/one",
        "@@ -1 +1 @@",
        "-1",
        "+2",
        "--- a/two",
        "+++ b/two",
        "@@ -1 +1 @@",
        "-3",
        "+4",
      ),
    );
    expect(files.map((f) => f.path)).toEqual(["one", "two"]);
  });

  it("returns nothing for an empty diff", () => {
    expect(parseUnifiedDiff("")).toEqual([]);
  });
});

describe("parseStatus", () => {
  it("reads codes, renames, and quoted paths", () => {
    const entries = parseStatus(
      [
        " M internal/client.go",
        "?? backoff.go",
        "R  old.go -> new.go",
        '?? "caf\\303\\251.txt"',
        "",
      ].join("\n"),
    );
    expect(entries).toEqual([
      { code: " M", path: "internal/client.go" },
      { code: "??", path: "backoff.go" },
      { code: "R ", path: "new.go", origPath: "old.go" },
      { code: "??", path: "café.txt" },
    ]);
  });

  it("counts changed, untracked, and conflicted files", () => {
    const entries = parseStatus(" M a\nA  b\n?? c\n?? d\nUU e\n");
    expect(summarizeStatus(entries)).toEqual({ changed: 2, untracked: 2, conflicted: 1 });
  });
});

describe("unquote", () => {
  it("leaves a plain path alone", () => {
    expect(unquote("a b.txt")).toBe("a b.txt");
  });

  it("reads C escapes", () => {
    expect(unquote('"tab\\there \\"q\\""')).toBe('tab\there "q"');
  });
});
