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
  /** sandbox is what the container may consume and reach. */
  sandbox: Sandbox;
  /** home marks the computer that holds the user's durable files. */
  home: boolean;
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

/** Sandbox is what a computer's container may consume and reach. */
export type Sandbox = {
  limits: SandboxLimits;
  egress: SandboxEgress;
};
