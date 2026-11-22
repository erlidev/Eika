/**
 * A subagent's question reaches Eika as a notice whose text is written for the
 * model: the question id, the options as JSON, and how to answer. The user
 * sees the question itself, so this reads those fields back out of the text
 * the server builds (`questionReason` and `AskSubagent` in internal/server).
 */

export type QuestionNotice = {
  /** name and agent say who asks. */
  name: string;
  agent: string;
  question: string;
  options: string[];
  allowFreeText: boolean;
};

const header = /^([a-z0-9-]+) \(([^)]+)\) asks: /;
const instruction = /\nAnswer with subagent_message[^\n]*$/;

/** parseQuestionNotice returns undefined for a notice that is not a question. */
export function parseQuestionNotice(text: string): QuestionNotice | undefined {
  const match = header.exec(text);
  if (!match?.[1] || !match[2]) return undefined;
  let body = text.slice(match[0].length).replace(instruction, "");
  body = body.replace(/^Question ID: [^\n]*\n/, "");
  let allowFreeText = true;
  const free = /\nFree text allowed: (true|false)$/.exec(body);
  if (free) {
    allowFreeText = free[1] === "true";
    body = body.slice(0, free.index);
  }
  let options: string[] = [];
  const listed = /\nOptions(?: \(JSON\))?: (\[.*\])$/s.exec(body);
  if (listed?.[1] !== undefined) {
    try {
      const parsed: unknown = JSON.parse(listed[1]);
      if (Array.isArray(parsed) && parsed.every((item) => typeof item === "string")) {
        options = parsed;
        body = body.slice(0, listed.index);
      }
    } catch {
      // Not JSON: leave the text as it came.
    }
  }
  return { name: match[1], agent: match[2], question: body.trim(), options, allowFreeText };
}
