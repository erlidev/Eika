import { describe, expect, it } from "vitest";

import type { MCPServer } from "@/api/types";
import { location, toolFlags, withDisabled } from "@/features/mcp/describe";

const server: MCPServer = {
  id: "mcp-1",
  name: "fs",
  kind: "stdio",
  url: "",
  header_names: null,
  command: "npx",
  args: ["-y", "@modelcontextprotocol/server-filesystem", "/srv/my code"],
  env_names: null,
  enabled: true,
  disabled_tools: ["write_file"],
  oauth_client_id: "",
  oauth_client_secret_set: false,
  state: "idle",
  workspaces: null,
  created_at: "2026-03-14T10:00:00Z",
  updated_at: "2026-03-14T10:00:00Z",
};

describe("toolFlags", () => {
  it("shows the claims a server made and nothing it left unsaid", () => {
    expect(toolFlags(undefined)).toEqual([]);
    expect(
      toolFlags({ read_only: null, destructive: null, idempotent: null, open_world: null }),
    ).toEqual([]);
    expect(
      toolFlags({ read_only: false, destructive: true, idempotent: false, open_world: true }),
    ).toEqual(["destructive", "open world"]);
  });

  it("does not call a read-only tool destructive", () => {
    expect(
      toolFlags({ read_only: true, destructive: true, idempotent: true, open_world: null }),
    ).toEqual(["read-only", "idempotent"]);
  });
});

describe("withDisabled", () => {
  it("turns a tool on and off, keeping the list sorted", () => {
    expect(withDisabled(server, "write_file", true)).toEqual([]);
    expect(withDisabled(server, "move_file", false)).toEqual(["move_file", "write_file"]);
    expect(withDisabled(server, "write_file", false)).toEqual(["write_file"]);
  });
});

describe("location", () => {
  it("quotes an argument with a space, so the line reads as the command it is", () => {
    expect(location(server)).toBe('npx -y @modelcontextprotocol/server-filesystem "/srv/my code"');
  });

  it("is an http server's URL", () => {
    expect(location({ ...server, kind: "http", url: "https://mcp.example.com/mcp" })).toBe(
      "https://mcp.example.com/mcp",
    );
  });
});
