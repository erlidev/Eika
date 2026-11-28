/**
 * Wire types for the HTTP API. They mirror the Go structs in
 * `internal/server` exactly; the contract is documented in docs/api/http.md.
 * When a Go type changes, change these in the same commit.
 *
 * The types live in `api/types/`, a file per domain like the Go handlers;
 * import them from here, `@/api/types`.
 */

export * from "@/api/types/auth";
export * from "@/api/types/instructions";
export * from "@/api/types/projects";
export * from "@/api/types/errors";
export * from "@/api/types/mcp";
export * from "@/api/types/memory";
export * from "@/api/types/providers";
export * from "@/api/types/requests";
export * from "@/api/types/runs";
export * from "@/api/types/search";
export * from "@/api/types/skills";
export * from "@/api/types/tripwires";
export * from "@/api/types/sessions";
export * from "@/api/types/settings";
export * from "@/api/types/halt";
export * from "@/api/types/withheld";
export * from "@/api/types/system";
export * from "@/api/types/computers";

/** AgentOverride replaces only class and prompt; null uses the built-in. */
export type AgentOverride = {
  class: "deep" | "main" | "fast" | null;
  prompt: string | null;
};

/** AgentDefinition holds the built-in identity, prompts and fixed capabilities. */
export type AgentDefinition = {
  name: string;
  description: string;
  class: "deep" | "main" | "fast";
  prompt: string;
  tools: string[];
  max_turns: number;
  compaction: { summary: string; update: string; turn_prefix: string };
};

/** Agent includes effective values and the defaults used by the editor. */
export type Agent = AgentDefinition & {
  builtin: AgentDefinition;
  overrides: AgentOverride;
};

/** Agents is the response from GET /api/agents. */
export type Agents = { agents: Agent[] };
