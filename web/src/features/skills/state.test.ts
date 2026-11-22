import { describe, expect, it } from "vitest";

import type { Skill, SkillInstall } from "@/api/types";
import { hitLabel, summarize, warningLabel } from "@/features/skills/state";

const skill = (over: Partial<Skill> = {}): Skill => ({
  name: "invoice-totals",
  description: "d",
  license: "",
  compatibility: "",
  metadata: {},
  allowed_tools: "",
  body: "",
  enabled: false,
  broken: "",
  sha: "s",
  installed_at: "2026-01-01T00:00:00Z",
  files: [],
  warnings: [],
  screen: { spans: [], max_score: 0, unscanned: false },
  ...over,
});

const result = (s: Skill, unchanged = false): SkillInstall => ({ skill: s, unchanged });

describe("skills state", () => {
  it("counts warnings and hits, saying nothing for none", () => {
    expect(warningLabel({ warning_count: 0 })).toBe("");
    expect(warningLabel({ warning_count: 1 })).toBe("1 warning");
    expect(hitLabel(2)).toBe("2 screen hits");
  });

  it("words a clean install, a flagged one and an unchanged one", () => {
    expect(summarize(result(skill())).tone).toBe("success");
    const flagged = summarize(
      result(
        skill({
          warnings: ["scripts/run.py is large"],
          screen: {
            spans: [{ field: "body", start: 0, end: 1, score: 0.9, text: "x" }],
            max_score: 0.9,
            unscanned: false,
          },
        }),
      ),
    );
    expect(flagged.tone).toBe("warning");
    expect(flagged.text).toContain("1 warning and 1 screen hit");
    expect(flagged.details).toEqual(["scripts/run.py is large"]);
    expect(summarize(result(skill(), true)).text).toContain("unchanged");
    expect(
      summarize(result(skill({ screen: { spans: [], max_score: 0, unscanned: true } }))).tone,
    ).toBe("warning");
  });
});
