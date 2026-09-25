import { describe, expect, it } from "vitest";

import type { MCPServer } from "@/api/types";
import {
  createInput,
  droppedHeaders,
  emptyForm,
  formFromServer,
  parseArgs,
  updateInput,
  validate,
} from "@/features/mcp/form";
import type { MCPFormState } from "@/features/mcp/form";

const stored: MCPServer = {
  id: "mcp-1",
  name: "github",
  kind: "http",
  url: "https://api.githubcopilot.com/mcp/",
  header_names: ["X-Api-Key"],
  command: "",
  args: null,
  env_names: null,
  enabled: true,
  disabled_tools: [],
  oauth_client_id: "",
  oauth_client_secret_set: false,
  state: "connected",
  workspaces: [],
  created_at: "2026-03-14T10:00:00Z",
  updated_at: "2026-03-14T10:00:00Z",
};

function http(overrides: Partial<MCPFormState> = {}): MCPFormState {
  return { ...emptyForm("http"), name: "github", url: "https://mcp.example.com/mcp", ...overrides };
}

describe("validate", () => {
  const cases: [string, MCPFormState, string | null][] = [
    ["a plain http server", http(), null],
    ["no name", http({ name: "" }), "name"],
    ["a double underscore, which would split the tool name", http({ name: "a__b" }), "name"],
    ["a space in the name", http({ name: "my server" }), "name"],
    ["a name of 33 characters", http({ name: "a".repeat(33) }), "name"],
    ["a taken name", http({ name: "linear" }), "name"],
    ["no URL", http({ url: "" }), "url"],
    ["a URL with credentials", http({ url: "https://u:p@mcp.example.com" }), "url"],
    ["a URL with a fragment", http({ url: "https://mcp.example.com/#x" }), "url"],
    ["a ws URL", http({ url: "ws://mcp.example.com" }), "url"],
    [
      "a header the client sets itself",
      http({ headers: [{ name: "Content-Type", value: "x", stored: false }] }),
      "headers",
    ],
    [
      "an Mcp-Param header",
      http({ headers: [{ name: "Mcp-Param-Repo", value: "x", stored: false }] }),
      "headers",
    ],
    [
      "a new header with no value",
      http({ headers: [{ name: "X-Api-Key", value: "", stored: false }] }),
      "headers",
    ],
    [
      "a stored header left blank, which keeps its value",
      http({ headers: [{ name: "X-Api-Key", value: "", stored: true }] }),
      null,
    ],
    [
      "the same header twice in another case",
      http({
        headers: [
          { name: "X-Key", value: "a", stored: false },
          { name: "x-key", value: "b", stored: false },
        ],
      }),
      "headers",
    ],
    ["a stdio server without a command", { ...emptyForm("stdio"), name: "fs" }, "command"],
    [
      "a variable starting with a digit",
      {
        ...emptyForm("stdio"),
        name: "fs",
        command: "npx",
        env: [{ name: "1X", value: "a", stored: false }],
      },
      "env",
    ],
  ];
  for (const [name, form, field] of cases) {
    it(name, () => {
      expect(validate(form, ["linear"])?.field ?? null).toBe(field);
    });
  }
});

describe("parseArgs", () => {
  it("keeps one argument per line, spaces included, and skips blank lines", () => {
    expect(parseArgs("-y\n@scope/server\n\n/path with space\r\n")).toEqual([
      "-y",
      "@scope/server",
      "/path with space",
    ]);
  });
});

describe("createInput", () => {
  it("sends a stdio server's command line and environment", () => {
    const form: MCPFormState = {
      ...emptyForm("stdio"),
      name: " fs ",
      command: "npx",
      args: "-y\n@modelcontextprotocol/server-filesystem\n.",
      env: [{ name: "LOG_LEVEL", value: "debug", stored: false }],
    };
    expect(createInput(form)).toEqual({
      name: "fs",
      kind: "stdio",
      command: "npx",
      args: ["-y", "@modelcontextprotocol/server-filesystem", "."],
      env: { LOG_LEVEL: "debug" },
    });
  });

  it("sends a client secret only with a client id", () => {
    expect(createInput(http({ clientSecret: "s3cret" }))).not.toHaveProperty("oauth_client_secret");
    expect(createInput(http({ clientId: "eika", clientSecret: "s3cret" }))).toMatchObject({
      oauth_client_id: "eika",
      oauth_client_secret: "s3cret",
    });
  });
});

describe("updateInput", () => {
  it("keeps a stored header left blank as null, and sends a typed one", () => {
    const form = formFromServer(stored);
    form.headers.push({ name: "X-Team", value: "core", stored: false });
    expect(updateInput(stored, form)).toEqual({ headers: { "X-Api-Key": null, "X-Team": "core" } });
  });

  it("leaves the stored headers behind when the URL changes, unless one is typed again", () => {
    const form = { ...formFromServer(stored), url: "https://mcp.example.org/mcp" };
    expect(droppedHeaders(stored, form)).toEqual(["X-Api-Key"]);
    expect(updateInput(stored, form)).toEqual({ url: "https://mcp.example.org/mcp", headers: {} });

    form.headers = [{ name: "X-Api-Key", value: "new", stored: true }];
    expect(droppedHeaders(stored, form)).toEqual([]);
    expect(updateInput(stored, form).headers).toEqual({ "X-Api-Key": "new" });
  });

  it("sends a renamed server's new name only", () => {
    expect(updateInput(stored, { ...formFromServer(stored), name: "gh" })).toEqual({
      name: "gh",
      headers: { "X-Api-Key": null },
    });
  });

  it("removes the stored client secret only when asked", () => {
    const withSecret = { ...stored, oauth_client_id: "eika", oauth_client_secret_set: true };
    const form = formFromServer(withSecret);
    expect(updateInput(withSecret, form)).not.toHaveProperty("oauth_client_secret");
    expect(updateInput(withSecret, { ...form, removeSecret: true })).toMatchObject({
      oauth_client_secret: "",
    });
  });
});
