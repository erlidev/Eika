/**
 * The Computer settings' rules, apart from React: the sandbox limits and
 * egress as the text their fields hold, the checks the harness makes before
 * it applies them, and how a limit reads. The bounds are the harness's (internal/server/sandbox.go);
 * the upper ones are the Docker host's, which GET /api/system reports.
 */

import type { EgressMode, SandboxEgress, SandboxHost, SandboxLimits } from "@/api/types";

/** The harness's bounds on a sandbox. */
export const bounds = {
  minCpus: 0.01,
  minMemoryMb: 64,
  minPids: 32,
  maxPids: 4194304,
  maxAllow: 200,
} as const;

/** SandboxSettings is what the Computer tab stores: the limits and the egress. */
export type SandboxSettings = { limits: SandboxLimits; egress: SandboxEgress };

/** SandboxDraft is the limits and egress as the editor's fields hold them. */
export type SandboxDraft = {
  cpus: string;
  memoryMb: string;
  pids: string;
  mode: EgressMode;
  allow: string[];
};

/** SandboxProblems names what keeps each field from saving. */
export type SandboxProblems = Partial<Record<"cpus" | "memoryMb" | "pids" | "allow", string>>;

/** egressModes are the modes in the order the editor offers them. */
export const egressModes: readonly { value: EgressMode; label: string; hint: string }[] = [
  {
    value: "open",
    label: "Open",
    hint: "The sandbox reaches the internet directly, as any container does.",
  },
  {
    value: "allowlist",
    label: "Allowlist",
    hint: "The sandbox reaches only the hosts listed below, through the harness's proxy. Tools that ignore HTTP_PROXY reach nothing.",
  },
  {
    value: "none",
    label: "None",
    hint: "The sandbox reaches the harness and its git hub, and nothing on the internet.",
  },
];

/** draftOf fills the editor from the stored limits and egress. */
export function draftOf(input: SandboxSettings): SandboxDraft {
  return {
    cpus: input.limits.cpus === 0 ? "" : String(input.limits.cpus),
    memoryMb: input.limits.memory_mb === 0 ? "" : String(input.limits.memory_mb),
    pids: input.limits.pids === 0 ? "" : String(input.limits.pids),
    mode: input.egress.mode,
    allow: [...(input.egress.allow ?? [])],
  };
}

/** number reads a field: "" is zero, which is no limit; anything else must be a number. */
function number(text: string): number {
  const trimmed = text.trim();
  return trimmed === "" ? 0 : Number(trimmed);
}

/**
 * checkDraft checks a draft as the harness will and returns what it would
 * store, or the problems that keep it from saving. host bounds the limits
 * when it knows the Docker host's capacity.
 */
export function checkDraft(
  draft: SandboxDraft,
  host: SandboxHost | undefined,
): { sandbox?: SandboxSettings; problems: SandboxProblems } {
  const problems: SandboxProblems = {};
  const cpus = number(draft.cpus);
  const maxCpus = host !== undefined && host.cpus > 0 ? host.cpus : Infinity;
  if (!Number.isFinite(cpus) || (cpus !== 0 && (cpus < bounds.minCpus || cpus > maxCpus))) {
    problems.cpus =
      maxCpus === Infinity
        ? `Enter a number of cores from ${String(bounds.minCpus)}, or leave it empty for no limit.`
        : `Enter a number of cores from ${String(bounds.minCpus)} to ${String(maxCpus)}, or leave it empty for no limit.`;
  }
  const memoryMb = number(draft.memoryMb);
  const maxMemoryMb =
    host !== undefined && host.memory_bytes > 0
      ? Math.floor(host.memory_bytes / 2 ** 20)
      : Infinity;
  if (
    !Number.isInteger(memoryMb) ||
    (memoryMb !== 0 && (memoryMb < bounds.minMemoryMb || memoryMb > maxMemoryMb))
  ) {
    problems.memoryMb =
      maxMemoryMb === Infinity
        ? `Enter whole MiB from ${String(bounds.minMemoryMb)}, or leave it empty for no limit.`
        : `Enter whole MiB from ${String(bounds.minMemoryMb)} to the host's ${String(maxMemoryMb)}, or leave it empty for no limit.`;
  }
  const pids = number(draft.pids);
  if (!Number.isInteger(pids) || (pids !== 0 && (pids < bounds.minPids || pids > bounds.maxPids))) {
    problems.pids = `Enter a whole number from ${String(bounds.minPids)} to ${String(bounds.maxPids)}, or leave it empty for no limit.`;
  }
  if (draft.allow.length > bounds.maxAllow) {
    problems.allow = `An allowlist holds at most ${String(bounds.maxAllow)} hosts.`;
  }
  if (draft.mode === "allowlist" && draft.allow.length === 0) {
    problems.allow = "An empty allowlist reaches nothing: add a host, or choose None.";
  }

  if (Object.keys(problems).length > 0) return { problems };
  return {
    problems,
    sandbox: {
      limits: { cpus, memory_mb: memoryMb, pids },
      egress: { mode: draft.mode, allow: draft.allow },
    },
  };
}

const ipv4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

/**
 * normalizePattern reads an allowlist entry as the harness stores it, or
 * says why it is not one: a host name such as github.com, a wildcard such as
 * *.github.com, or an IP address. A pasted URL is reduced to its host.
 */
export function normalizePattern(input: string): { pattern?: string; problem?: string } {
  let p = input.trim().toLowerCase();
  if (p.includes("://")) {
    try {
      p = new URL(p).hostname;
    } catch {
      return { problem: `${input.trim()} is not a host name such as github.com.` };
    }
  }
  if (p === "" || p === "*") {
    return { problem: "Enter a host such as github.com; to allow every host, choose Open." };
  }
  if (p.length > 253) return { problem: "A host name is at most 253 characters." };
  if (ipv4.test(p) || (p.includes(":") && /^[0-9a-f:.]+$/.test(p))) return { pattern: p };
  const name = p.startsWith("*.") ? p.slice(2) : p;
  const labels = name.split(".");
  const valid = labels.every(
    (l) =>
      l.length > 0 &&
      l.length <= 63 &&
      /^[a-z0-9-]+$/.test(l) &&
      !l.startsWith("-") &&
      !l.endsWith("-"),
  );
  if (!valid) {
    return { problem: `${input.trim()} is not a host name such as github.com or *.github.com.` };
  }
  return { pattern: p };
}

/** formatCores reads a CPU limit: "No limit", "0.5 cores", "1 core". */
export function formatCores(cpus: number): string {
  if (cpus === 0) return "No limit";
  return `${String(Math.round(cpus * 100) / 100)} ${cpus === 1 ? "core" : "cores"}`;
}

/** formatMemory reads a memory limit in MiB: "No limit", "512 MiB", "1.5 GiB". */
export function formatMemory(mb: number): string {
  if (mb === 0) return "No limit";
  if (mb < 1024) return `${String(mb)} MiB`;
  return `${String(Math.round((mb / 1024) * 100) / 100)} GiB`;
}

/** formatPids reads a process limit. */
export function formatPids(pids: number): string {
  return pids === 0 ? "No limit" : `${pids.toLocaleString("en-US")} processes`;
}
