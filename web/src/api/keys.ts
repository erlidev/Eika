/**
 * The query keys every feature shares. They live here so that the event
 * subscriber in `app/` can invalidate a feature's data without importing it.
 */

/** queryKeys names the cached server state, one entry per route. */
export const queryKeys = {
  /** allSessions is every session, which Activity reads the subagents of; it sits under the sessions prefix too. */
  allSessions: () => ["sessions", "all"] as const,
  /** computers is the list of computers, of which the session view reads the home one. */
  computers: () => ["computers"] as const,
  /** home is the one home session, the conversation Chat shows. */
  home: () => ["home"] as const,
  session: (id: string) => ["session", id] as const,
  sessionOutline: (id: string) => ["session", id, "outline"] as const,
  runStatus: (sessionId: string) => ["session", sessionId, "run"] as const,
  /** sessionContext is the prefix of what the session's next request is expected to be. */
  sessionContext: (id: string) => ["session", id, "context"] as const,
  /**
   * sessionPrediction sits under the context prefix, so whatever invalidates the
   * session's context (a turn that ends, a compaction) invalidates its
   * prediction too.
   */
  sessionPrediction: (id: string, model: string) =>
    ["session", id, "context", "prediction", model] as const,
  /** projects is the list of projects, which name what a subagent works on. */
  projects: () => ["projects"] as const,
  projectInstructions: (id: string) => ["project", id, "instructions"] as const,
  /** agents is the built-in agents with the user's overrides. */
  agents: () => ["agents"] as const,
  /** instructions is the user's global About me and rules. */
  instructions: () => ["instructions"] as const,
  settings: () => ["settings"] as const,
  /** halt is whether Eika is halted. */
  halt: () => ["halt"] as const,
  guard: () => ["guard"] as const,
  blockedHosts: () => ["blocked-hosts"] as const,
  trustedSources: () => ["trusted-sources"] as const,
  /** withheld is the list of removals, newest first, paged. */
  withheld: () => ["withheld"] as const,
  /** memories without arguments is the prefix of every memory query. */
  memories: () => ["memories"] as const,
  memory: (name: string) => ["memories", name] as const,
  models: () => ["models"] as const,
  providers: () => ["providers"] as const,
  system: () => ["system"] as const,
  searchStatus: () => ["search", "status"] as const,
  /** mcpServers without an id is the prefix of every MCP server query. */
  mcpServers: () => ["mcp"] as const,
  mcpServer: (id: string) => ["mcp", id] as const,
  authStatus: () => ["auth", "status"] as const,
};
