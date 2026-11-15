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
const researcherSummary = readFileSync(
  new URL("../../../internal/agentdef/prompts/researcher-summary.md", import.meta.url),
  "utf8",
);
const researcherUpdate = readFileSync(
  new URL("../../../internal/agentdef/prompts/researcher-update.md", import.meta.url),
  "utf8",
);
const researcherTurnPrefix = readFileSync(
  new URL("../../../internal/agentdef/prompts/researcher-turn-prefix.md", import.meta.url),
  "utf8",
);
const worker = readFileSync(
  new URL("../../../internal/agentdef/prompts/worker.md", import.meta.url),
  "utf8",
);
const workerSummary = readFileSync(
  new URL("../../../internal/agentdef/prompts/worker-summary.md", import.meta.url),
  "utf8",
);
const workerUpdate = readFileSync(
  new URL("../../../internal/agentdef/prompts/worker-update.md", import.meta.url),
  "utf8",
);
const workerTurnPrefix = readFileSync(
  new URL("../../../internal/agentdef/prompts/worker-turn-prefix.md", import.meta.url),
  "utf8",
);

export const builtinAgents: AgentDefinition[] = [
  {
    name: "eika",
    description: "Converses, plans and delegates work.",
    class: "main",
    max_turns: 40,
    tools: ["web_search"],
    prompt: eika,
    compaction: { summary: eikaSummary, update: eikaUpdate, turn_prefix: eikaTurnPrefix },
  },
  {
    name: "researcher",
    description: "Reads the web and reports findings.",
    class: "fast",
    max_turns: 200,
    tools: ["ask_user", "web_fetch", "web_search"],
    prompt: researcher,
    compaction: {
      summary: researcherSummary,
      update: researcherUpdate,
      turn_prefix: researcherTurnPrefix,
    },
  },
  {
    name: "worker",
    description: "Does work on a computer and reports results.",
    class: "main",
    max_turns: 200,
    tools: ["ask_user", "bash", "file_edit", "file_read", "file_write", "web_fetch", "web_search"],
    prompt: worker,
    compaction: { summary: workerSummary, update: workerUpdate, turn_prefix: workerTurnPrefix },
  },
];
