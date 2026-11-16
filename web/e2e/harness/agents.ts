/** Built-in agent fixtures use the same embedded Markdown as the Go definitions. */
import { readFileSync } from "node:fs";
import { URL } from "node:url";
import type { AgentDefinition } from "../../src/api/types.ts";
const eika = readFileSync(
  new URL("../../../internal/agentdef/prompts/eika.md", import.meta.url),
  "utf8",
);
const eikaSummary = readFileSync(
  new URL("../../../internal/agentdef/prompts/eika-summary.md", import.meta.url),
  "utf8",
);
const eikaUpdate = readFileSync(
  new URL("../../../internal/agentdef/prompts/eika-update.md", import.meta.url),
  "utf8",
);
const eikaTurnPrefix = readFileSync(
  new URL("../../../internal/agentdef/prompts/eika-turn-prefix.md", import.meta.url),
  "utf8",
);
const researcher = readFileSync(
  new URL("../../../internal/agentdef/prompts/researcher.md", import.meta.url),
  "utf8",
);
const worker = readFileSync(
  new URL("../../../internal/agentdef/prompts/worker.md", import.meta.url),
  "utf8",
);

/**
 * Researcher and worker use the Go built-in compaction prompts plus one
 * closing line (`preserving` in internal/agentdef). The UI never shows these
 * texts, so the mock stands in for the built-in part.
 */
function preserving(line: string) {
  const tail = `\n\n${line}\n`;
  return {
    summary: `Built-in summary prompt.${tail}`,
    update: `Built-in update prompt.${tail}`,
    turn_prefix: `Built-in turn prefix prompt.${tail}`,
  };
}

export const builtinAgents: AgentDefinition[] = [
  {
    name: "eika",
    description: "Converses, plans and delegates work.",
    class: "main",
    max_turns: 40,
    tools: [
      "project_create",
      "project_list",
      "project_open",
      "project_update",
      "subagent_cancel",
      "subagent_message",
      "subagent_start",
      "subagent_status",
      "web_search",
    ],
    prompt: eika,
    compaction: { summary: eikaSummary, update: eikaUpdate, turn_prefix: eikaTurnPrefix },
  },
  {
    name: "researcher",
    description: "Reads the web and reports findings.",
    class: "fast",
    max_turns: 200,
    tools: ["ask_user", "report", "web_fetch", "web_search"],
    prompt: researcher,
    compaction: preserving(
      "Preserve the research question, findings, source URLs, uncertainty and open questions.",
    ),
  },
  {
    name: "worker",
    description: "Does work on a computer and reports results.",
    class: "main",
    max_turns: 200,
    tools: [
      "ask_user",
      "bash",
      "file_edit",
      "file_read",
      "file_write",
      "report",
      "web_fetch",
      "web_search",
    ],
    prompt: worker,
    compaction: preserving(
      "Preserve the brief, changed files, commands, checks, errors and remaining work.",
    ),
  },
];
