import { describe, expect, it } from "vitest";

import {
  describeArgument,
  firstLine,
  formatAgo,
  formatDuration,
  formatTokens,
  shortId,
} from "@/lib/format";

describe("formatDuration", () => {
  it("renders milliseconds, seconds, and minutes", () => {
    expect(formatDuration(12)).toBe("12ms");
    expect(formatDuration(1500)).toBe("1.5s");
    expect(formatDuration(45_000)).toBe("45s");
    expect(formatDuration(95_000)).toBe("1m 35s");
  });

  it("renders nothing for a value that is not a duration", () => {
    expect(formatDuration(Number.NaN)).toBe("");
    expect(formatDuration(-1)).toBe("");
  });
});

describe("formatTokens", () => {
  it("suffixes thousands and millions", () => {
    expect(formatTokens(42)).toBe("42");
    expect(formatTokens(4200)).toBe("4.2k");
    expect(formatTokens(42_000)).toBe("42k");
    expect(formatTokens(4_200_000)).toBe("4.2M");
  });
});

describe("formatAgo", () => {
  const now = Date.parse("2026-01-02T00:00:00Z");

  it("renders each unit", () => {
    expect(formatAgo("2026-01-01T23:59:30Z", now)).toBe("just now");
    expect(formatAgo("2026-01-01T23:30:00Z", now)).toBe("30m ago");
    expect(formatAgo("2026-01-01T20:00:00Z", now)).toBe("4h ago");
    expect(formatAgo("2025-12-30T00:00:00Z", now)).toBe("3d ago");
  });

  it("renders nothing for an unparsable time", () => {
    expect(formatAgo("not a time", now)).toBe("");
  });
});

describe("shortId", () => {
  it("truncates a long identifier", () => {
    expect(shortId("abcdefghijkl")).toBe("abcdefgh");
    expect(shortId("abc")).toBe("abc");
  });
});

describe("describeArgument", () => {
  it("renders scalars as themselves and objects as JSON", () => {
    expect(describeArgument("ls -la")).toBe("ls -la");
    expect(describeArgument(3)).toBe("3");
    expect(describeArgument(undefined)).toBe("");
    expect(describeArgument({ a: 1 })).toBe('{"a":1}');
  });
});

describe("firstLine", () => {
  it("keeps the first line and ellipsises a long one", () => {
    expect(firstLine("one\ntwo")).toBe("one");
    expect(firstLine("x".repeat(10), 5)).toBe("xxxx…");
  });
});
