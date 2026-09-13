/**
 * The query keys every feature shares. They live here so that the event
 * subscriber in `app/` can invalidate a feature's data without importing it.
 */

/** queryKeys names the cached server state, one entry per route. */
export const queryKeys = {
  projects: () => ["projects"] as const,
  project: (id: string) => ["projects", id] as const,
  workspaces: (projectId?: string) => ["workspaces", projectId ?? "all"] as const,
  workspace: (id: string) => ["workspace", id] as const,
  workspaceDiff: (id: string) => ["workspace", id, "diff"] as const,
  sessions: (workspaceId?: string) => ["sessions", workspaceId ?? "all"] as const,
  session: (id: string) => ["session", id] as const,
  sessionOutline: (id: string) => ["session", id, "outline"] as const,
  sessionPath: (id: string) => ["session", id, "path"] as const,
  runStatus: (sessionId: string) => ["session", sessionId, "run"] as const,
  settings: () => ["settings"] as const,
  models: () => ["models"] as const,
};
