/** Wire types for the system check and the sandbox host. */

/** SystemStatus is the body of GET /api/system. */
export type SystemStatus = {
  docker: { reachable: boolean; error?: string };
  sandbox_image: { name: string; present: boolean };
  /** sandbox is what the Docker host can give a computer. */
  sandbox: SandboxHost;
  providers: number;
  models: number;
};

/** SandboxHost is what the Docker host has, which bounds a sandbox's limits. */
export type SandboxHost = {
  /** cpus and memory_bytes are zero when Docker cannot be asked. */
  cpus: number;
  memory_bytes: number;
  /** egress_control is false for a harness outside compose, which cannot restrict egress. */
  egress_control: boolean;
};
