import { describe, expect, it } from "vitest";

import type { Sandbox, SandboxHost } from "@/api/types";
import {
  checkDraft,
  draftOf,
  formatCores,
  formatMemory,
  formatPids,
  normalizePattern,
  sameSandbox,
} from "@/features/sandbox/form";
import type { SandboxDraft } from "@/features/sandbox/form";

const host: SandboxHost = { cpus: 8, memory_bytes: 16 * 2 ** 30, egress_control: true };

const stored: Sandbox = {
  limits: { cpus: 2, memory_mb: 4096, pids: 0 },
  egress: { mode: "allowlist", allow: ["github.com"] },
  ports: [{ port: 5173, label: "vite" }],
};

function draft(change: Partial<SandboxDraft>): SandboxDraft {
  return { ...draftOf(stored), ...change };
}

describe("draftOf and checkDraft", () => {
  it("round-trips a stored sandbox, with no limit as an empty field", () => {
    const d = draftOf(stored);
    expect(d.pids).toBe("");
    expect(d.ports).toEqual([{ port: "5173", label: "vite" }]);
    const { sandbox, problems } = checkDraft(d, host);
    expect(problems).toEqual({});
    if (sandbox === undefined) throw new Error("a stored sandbox did not check");
    expect(sameSandbox(sandbox, stored)).toBe(true);
  });

  it("holds limits to the host and the harness's bounds", () => {
    expect(checkDraft(draft({ cpus: "9" }), host).problems.cpus).toMatch(/to 8/);
    expect(checkDraft(draft({ cpus: "0.001" }), host).problems.cpus).toBeDefined();
    expect(checkDraft(draft({ cpus: "abc" }), host).problems.cpus).toBeDefined();
    expect(checkDraft(draft({ memoryMb: "32" }), host).problems.memoryMb).toMatch(/from 64/);
    expect(checkDraft(draft({ memoryMb: "20000" }), host).problems.memoryMb).toMatch(/16384/);
    expect(checkDraft(draft({ memoryMb: "512.5" }), host).problems.memoryMb).toBeDefined();
    expect(checkDraft(draft({ pids: "8" }), host).problems.pids).toBeDefined();
    // A host that could not be asked bounds nothing from above.
    expect(checkDraft(draft({ cpus: "64" }), undefined).problems.cpus).toBeUndefined();
  });

  it("refuses an allowlist that reaches nothing", () => {
    expect(checkDraft(draft({ allow: [] }), host).problems.allow).toMatch(/reaches nothing/);
    expect(checkDraft(draft({ mode: "none", allow: [] }), host).problems.allow).toBeUndefined();
  });

  it("checks the forwarded ports", () => {
    const ports = (...list: string[]) =>
      draft({ ports: list.map((port) => ({ port, label: "" })) });
    expect(checkDraft(ports("3000", "3000"), host).problems.ports).toMatch(/twice/);
    expect(checkDraft(ports("7000"), host).problems.ports).toMatch(/daemon/);
    expect(checkDraft(ports("0"), host).problems.ports).toBeDefined();
    expect(checkDraft(ports(""), host).problems.ports).toMatch(/A port/);
    expect(checkDraft(ports("3000"), host).sandbox?.ports).toEqual([{ port: 3000 }]);
  });
});

describe("normalizePattern", () => {
  it.each([
    ["github.com", "github.com"],
    [" GitHub.com ", "github.com"],
    ["*.githubusercontent.com", "*.githubusercontent.com"],
    ["https://registry.npmjs.org/some/package", "registry.npmjs.org"],
    ["203.0.113.5", "203.0.113.5"],
  ])("reads %s as %s", (input, want) => {
    expect(normalizePattern(input)).toEqual({ pattern: want });
  });

  it.each(["", "*", "github.com:443", "exa mple.com", "-a.com", "a..b", "*.*.com"])(
    "refuses %j",
    (input) => {
      expect(normalizePattern(input).problem).toBeDefined();
    },
  );
});

describe("formatting", () => {
  it("reads limits as a person says them", () => {
    expect(formatCores(0)).toBe("No limit");
    expect(formatCores(1)).toBe("1 core");
    expect(formatCores(2.5)).toBe("2.5 cores");
    expect(formatMemory(0)).toBe("No limit");
    expect(formatMemory(512)).toBe("512 MiB");
    expect(formatMemory(1536)).toBe("1.5 GiB");
    expect(formatPids(4096)).toBe("4,096 processes");
  });
});
