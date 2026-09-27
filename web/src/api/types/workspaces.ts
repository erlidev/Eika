/** Wire types for workspaces and their sandboxes. */

/** WorkspaceLifecycle is a workspace's lifecycle state. */
export type WorkspaceLifecycle = "creating" | "running" | "stopped" | "gone";

/**
 * Workspace is a checkout of a project: one sandbox container holding it, or
 * a git worktree in another workspace's container (worktree_of).
 */
export type Workspace = {
  id: string;
  project_id: string;
  name: string;
  branch: string;
  base_commit?: string;
  image: string;
  state: WorkspaceLifecycle;
  container_id?: string;
  parent_workspace_id?: string;
  /**
   * worktree_of names the workspace whose container this one is a git
   * worktree in. It starts, stops, and is confined with that workspace, and
   * its own sandbox is empty.
   */
  worktree_of?: string;
  /** sandbox is what the container may consume, reach, and expose. */
  sandbox: Sandbox;
  created_at: string;
  updated_at: string;
};

/** SandboxLimits bound a container's resources. Zero is no limit. */
export type SandboxLimits = {
  /** cpus is how many cores it may use, as a fraction. */
  cpus: number;
  /** memory_mb is its memory in MiB, with no swap beyond it. */
  memory_mb: number;
  /** pids is how many processes and threads it may have. */
  pids: number;
};

/**
 * EgressMode is what a sandbox may reach: the internet directly, only the
 * hosts its allowlist names through the harness's proxy, or nothing but the
 * harness.
 */
export type EgressMode = "open" | "allowlist" | "none";

/** SandboxEgress is a sandbox's egress mode and allowlist. */
export type SandboxEgress = {
  mode: EgressMode;
  /** allow holds host patterns such as github.com or *.githubusercontent.com. */
  allow: string[] | null;
};

/** SandboxPort is a container port the harness forwards previews to. */
export type SandboxPort = {
  port: number;
  label?: string;
};

/** Sandbox is what a workspace's container may consume, reach, and expose. */
export type Sandbox = {
  limits: SandboxLimits;
  egress: SandboxEgress;
  ports: SandboxPort[] | null;
};

/** WorkspaceUsage is the body of GET /api/workspaces/{id}/usage: one sample. */
export type WorkspaceUsage = {
  /** cpu_percent is the CPU used over the sample, 100 per core. */
  cpu_percent: number;
  /** cpus is the cores the container may use: its limit, or the host's. */
  cpus: number;
  memory_bytes: number;
  memory_limit_bytes: number;
  pids: number;
  /** pids_limit is zero when the container has none. */
  pids_limit: number;
  network_rx_bytes: number;
  network_tx_bytes: number;
  sampled_at: string;
  /** blocked are the hosts the egress proxy refused lately, most recent first. */
  blocked: BlockedHost[] | null;
};

/** BlockedHost is a host the egress proxy refused a workspace. */
export type BlockedHost = {
  host: string;
  count: number;
  last: string;
};

/** PreviewLink is the body of POST /api/workspaces/{id}/ports/{port}/preview. */
export type PreviewLink = {
  /** url opens the preview; its ticket works once, within two minutes. */
  url: string;
};

/** CreateWorkspace is the body of POST /api/workspaces. */
export type CreateWorkspace = {
  project_id: string;
  name: string;
  branch?: string;
  image?: string;
  build_context?: string;
  dockerfile?: string;
  parent_workspace_id?: string;
  /** sandbox left out gives the new workspace the settings' defaults. */
  sandbox?: Sandbox;
};
