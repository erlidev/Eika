/** Wire types for computers and their sandboxes. */

/** ComputerLifecycle is a computer's lifecycle state. */
export type ComputerLifecycle = "creating" | "running" | "stopped" | "gone";

/** Computer is one sandbox container with a volume of its own. */
export type Computer = {
  id: string;
  name: string;
  image: string;
  state: ComputerLifecycle;
  container_id?: string;
  /** sandbox is what the container may consume, reach, and expose. */
  sandbox: Sandbox;
  /** pinned computers are listed before the others. */
  pinned: boolean;
  /** archived computers are set aside under an Archived heading; the container is left as it is. */
  archived: boolean;
  /** home marks the computer that holds the user's durable files; it cannot be deleted. */
  home: boolean;
  created_at: string;
  updated_at: string;
};

/** UpdateComputer is the body of PATCH /api/computers/{id}; an absent field is left alone. */
export type UpdateComputer = {
  name?: string;
  pinned?: boolean;
  archived?: boolean;
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

/** Sandbox is what a computer's container may consume and reach. */
export type Sandbox = {
  limits: SandboxLimits;
  egress: SandboxEgress;
};

/** ComputerUsage is the body of GET /api/computers/{id}/usage: one sample. */
export type ComputerUsage = {
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

/** BlockedHost is a host the egress proxy refused a computer. */
export type BlockedHost = {
  host: string;
  count: number;
  last: string;
};

/** CreateComputer is the body of POST /api/computers. */
export type CreateComputer = {
  name: string;
  image?: string;
  build_context?: string;
  dockerfile?: string;
  /** sandbox left out gives the new computer the settings' defaults. */
  sandbox?: Sandbox;
};
