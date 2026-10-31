/**
 * The query keys every feature shares. They live here so that the event
 * subscriber in `app/` can invalidate a feature's data without importing it.
 */

/** queryKeys names the cached server state, one entry per route. */
export const queryKeys = {
  /** chats sits under the sessions prefix, so invalidating sessions refreshes it. */
  chats: () => ["sessions", "chats"] as const,
  /** computerSessions sits under the sessions prefix too, for the same reason. */
  computerSessions: (computerId: string) => ["sessions", "computer", computerId] as const,
  /** computers is the list of computers, of which the sidebar reads the home one. */
  computers: () => ["computers"] as const,
  session: (id: string) => ["session", id] as const,
  sessionOutline: (id: string) => ["session", id, "outline"] as const,
  runStatus: (sessionId: string) => ["session", sessionId, "run"] as const,
  /** sessionConfiguration without a draft is the saved one and the prefix of every draft's. */
  sessionConfiguration: (id: string, draft?: { profileId?: string; modelId?: string }) =>
    draft === undefined
      ? (["session", id, "configuration"] as const)
      : (["session", id, "configuration", draft.profileId ?? null, draft.modelId ?? null] as const),
  /** sessionContext without a model is the prefix of every preview of the session. */
  sessionContext: (id: string, model?: string) =>
    model === undefined
      ? (["session", id, "context"] as const)
      : (["session", id, "context", model] as const),
  /** sessionRequests is the prefix of the session's recorded requests and each one. */
  sessionRequests: (id: string) => ["session", id, "requests"] as const,
  sessionRequest: (id: string, requestId: string) =>
    ["session", id, "requests", requestId] as const,
  profiles: () => ["profiles"] as const,
  /** profileInherited sits under the profiles prefix, so a profile change refreshes it. */
  profileInherited: (modelId: string) => ["profiles", "inherited", modelId] as const,
  settings: () => ["settings"] as const,
  models: () => ["models"] as const,
  providers: () => ["providers"] as const,
  system: () => ["system"] as const,
  searchStatus: () => ["search", "status"] as const,
  tools: () => ["tools"] as const,
  /** mcpServers without an id is the prefix of every MCP server query. */
  mcpServers: () => ["mcp"] as const,
  mcpServer: (id: string) => ["mcp", id] as const,
  authStatus: () => ["auth", "status"] as const,
};
