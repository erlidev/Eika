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
  /** workspaceFiles without a path is the prefix of every listing of the workspace. */
  workspaceFiles: (id: string, path?: string) =>
    path === undefined
      ? (["workspace", id, "files"] as const)
      : (["workspace", id, "files", path] as const),
  workspaceFile: (id: string, path: string) => ["workspace", id, "file", path] as const,
  sessions: (workspaceId?: string) => ["sessions", workspaceId ?? "all"] as const,
  session: (id: string) => ["session", id] as const,
  sessionOutline: (id: string) => ["session", id, "outline"] as const,
  sessionPath: (id: string) => ["session", id, "path"] as const,
  runStatus: (sessionId: string) => ["session", sessionId, "run"] as const,
  settings: () => ["settings"] as const,
  models: () => ["models"] as const,
  providers: () => ["providers"] as const,
  system: () => ["system"] as const,
  searchStatus: () => ["search", "status"] as const,
  authStatus: () => ["auth", "status"] as const,
};
