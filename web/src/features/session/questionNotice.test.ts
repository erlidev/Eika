import { describe, expect, it } from "vitest";

import { parseQuestionNotice } from "@/features/session/questionNotice";

describe("parseQuestionNotice", () => {
  it("reads the server's question notice", () => {
    const text =
      'permit (researcher) asks: Question ID: question-ab\nWhich council?\nOptions (JSON): ["City","County"]\nFree text allowed: false\nAnswer with subagent_message and question_id question-ab, or ask the user in your reply and relay the answer with that id.';
    expect(parseQuestionNotice(text)).toEqual({
      name: "permit",
      agent: "researcher",
      question: "Which council?",
      options: ["City", "County"],
      allowFreeText: false,
    });
  });

  it("keeps a free-text question whole and ignores other notices", () => {
    const text =
      "permit (researcher) asks: Question ID: q\nLine one\nLine two\nAnswer with subagent_message and question_id q, or ask.";
    expect(parseQuestionNotice(text)?.question).toBe("Line one\nLine two");
    expect(parseQuestionNotice("plumber (researcher) reported: Done.")).toBeUndefined();
  });
});
