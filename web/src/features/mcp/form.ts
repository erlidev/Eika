/**
 * The rules behind the MCP server form: what it checks before it asks the
 * harness, and what a save sends. Header and environment values never come
 * back from the harness, only their names, so a stored pair is shown with an
 * empty value and saved as null, which keeps what is stored.
 */

import type { CreateMCPServer, MCPServer, MCPServerKind, UpdateMCPServer } from "@/api/types";

/** maxServerName is the longest server name the harness accepts. */
export const maxServerName = 32;

/** Pair is one header or environment variable in the form. */
export type Pair = {
  name: string;
  value: string;
  /** stored says the harness holds a value under this name, which the form cannot show. */
  stored: boolean;
};

/** MCPFormState is what the form holds. */
export type MCPFormState = {
  kind: MCPServerKind;
  name: string;
  url: string;
  headers: Pair[];
  command: string;
  /** args is one argument per line, so an argument may hold spaces. */
  args: string;
  env: Pair[];
  clientId: string;
  clientSecret: string;
  /** removeSecret is the user's choice to drop the stored client secret. */
  removeSecret: boolean;
};

/** MCPField names a field of the form. */
export type MCPField = "name" | "url" | "headers" | "command" | "env" | "clientSecret";

/** MCPProblem is what keeps the form from saving, and where. */
export type MCPProblem = { field: MCPField; message: string };

/** serverNamePattern is the harness's: letters, digits, hyphens, and single underscores between them. */
const serverNamePattern = /^[A-Za-z0-9-]+(_[A-Za-z0-9-]+)*$/;

/** envNamePattern is what an environment variable may be called. */
const envNamePattern = /^[A-Za-z_][A-Za-z0-9_]*$/;

/** headerNamePattern is an HTTP field name, RFC 9110's token. */
const headerNamePattern = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;

/** clientHeaders are the headers the MCP client sets itself, which a configuration may not replace. */
const clientHeaders = new Set([
  "accept",
  "connection",
  "content-length",
  "content-type",
  "host",
  "last-event-id",
  "mcp-method",
  "mcp-name",
  "mcp-protocol-version",
  "mcp-session-id",
  "transfer-encoding",
]);

/** emptyForm is a new server of a kind. */
export function emptyForm(kind: MCPServerKind = "http"): MCPFormState {
  return {
    kind,
    name: "",
    url: "",
    headers: [],
    command: "",
    args: "",
    env: [],
    clientId: "",
    clientSecret: "",
    removeSecret: false,
  };
}

/** formFromServer is the form for changing a stored server. */
export function formFromServer(server: MCPServer): MCPFormState {
  const stored = (names: string[] | null): Pair[] =>
    (names ?? []).map((name) => ({ name, value: "", stored: true }));
  return {
    kind: server.kind,
    name: server.name,
    url: server.url,
    headers: stored(server.header_names),
    command: server.command,
    args: (server.args ?? []).join("\n"),
    env: stored(server.env_names),
    clientId: server.oauth_client_id,
    clientSecret: "",
    removeSecret: false,
  };
}

/** parseArgs splits the arguments box into one argument per non-empty line. */
export function parseArgs(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.replace(/\r$/, ""))
    .filter((line) => line.trim() !== "");
}

/**
 * urlWillDropSecrets reports whether saving drops what the harness stored for
 * the old URL: its headers, unless the form sends them again, and the OAuth
 * tokens, which were issued for that URL alone.
 */
export function urlWillDropSecrets(server: MCPServer | undefined, form: MCPFormState): boolean {
  return server?.kind === "http" && form.url.trim() !== server.url;
}

/** urlProblem mirrors the harness's check of a server URL. */
export function urlProblem(raw: string): string | null {
  const text = raw.trim();
  if (text === "") return "Enter the server's URL, such as https://mcp.example.com/mcp.";
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    return "The URL must be a full URL starting with http:// or https://.";
  }
  if ((url.protocol !== "http:" && url.protocol !== "https:") || url.host === "") {
    return "The URL must start with http:// or https://.";
  }
  if (url.username !== "" || url.password !== "") {
    return "Take the credentials out of the URL; send them as a header instead.";
  }
  if (url.hash !== "" || text.includes("#")) return "Remove the fragment (the part from #).";
  return null;
}

/** pairProblem is the first thing wrong with a list of headers or variables. */
function pairProblem(pairs: Pair[], what: "header" | "variable"): string | null {
  const seen = new Set<string>();
  for (const pair of pairs) {
    const name = pair.name.trim();
    if (name === "") return `Give every ${what} a name, or remove the empty row.`;
    const key = what === "header" ? name.toLowerCase() : name;
    if (seen.has(key)) return `${name} is listed twice.`;
    seen.add(key);
    if (what === "header") {
      if (!headerNamePattern.test(name)) return `${name} is not a valid header name.`;
      if (clientHeaders.has(key) || key.startsWith("mcp-param-")) {
        return `${name} is set by the MCP client itself and cannot be configured.`;
      }
    } else if (!envNamePattern.test(name)) {
      return `${name} is not a valid variable name: use letters, digits, and underscores, not starting with a digit.`;
    }
    if (!pair.stored && pair.value === "") return `Enter a value for ${name}.`;
  }
  if (pairs.length > 64) return `At most 64 ${what}s.`;
  return null;
}

/** validate mirrors what the harness refuses, so the save button says why it waits. */
export function validate(form: MCPFormState, takenNames: readonly string[]): MCPProblem | null {
  const name = form.name.trim();
  if (name === "") return { field: "name", message: "Give the server a name." };
  if (name.length > maxServerName) {
    return { field: "name", message: `Shorten the name to ${String(maxServerName)} characters.` };
  }
  if (!serverNamePattern.test(name)) {
    return {
      field: "name",
      message:
        "Use letters, digits, hyphens, and single underscores: the name is part of every tool name, as mcp__name__tool.",
    };
  }
  if (takenNames.includes(name)) {
    return { field: "name", message: "Another server has this name; choose a different one." };
  }
  if (form.kind === "http") {
    const url = urlProblem(form.url);
    if (url) return { field: "url", message: url };
    const headers = pairProblem(form.headers, "header");
    if (headers) return { field: "headers", message: headers };
    return null;
  }
  if (form.command.trim() === "") {
    return { field: "command", message: "Enter the command that starts the server, such as npx." };
  }
  const env = pairProblem(form.env, "variable");
  if (env) return { field: "env", message: env };
  return null;
}

/** newValues is a list of pairs as a create sends them. */
function newValues(pairs: Pair[]): Record<string, string> {
  return Object.fromEntries(pairs.map((p) => [p.name.trim(), p.value]));
}

/**
 * keptValues is a list of pairs as an update sends them: a stored pair left
 * blank is null, which keeps its value; a blank pair with no stored value
 * never reaches here, since validate refuses it.
 */
function keptValues(pairs: Pair[]): Record<string, string | null> {
  return Object.fromEntries(
    pairs.map((p) => [p.name.trim(), p.stored && p.value === "" ? null : p.value]),
  );
}

/** createInput is what adding the server sends. */
export function createInput(form: MCPFormState): CreateMCPServer {
  const base = { name: form.name.trim(), kind: form.kind };
  if (form.kind === "stdio") {
    return {
      ...base,
      command: form.command.trim(),
      args: parseArgs(form.args),
      env: newValues(form.env),
    };
  }
  const clientId = form.clientId.trim();
  const secret = form.clientSecret.trim();
  return {
    ...base,
    url: form.url.trim(),
    headers: newValues(form.headers),
    ...(clientId === "" ? {} : { oauth_client_id: clientId }),
    ...(clientId === "" || secret === "" ? {} : { oauth_client_secret: secret }),
  };
}

/**
 * droppedHeaders are the stored headers a save leaves behind because the URL
 * changed and no new value was typed for them: a header is only ever sent to
 * the URL it was entered for.
 */
export function droppedHeaders(server: MCPServer | undefined, form: MCPFormState): string[] {
  if (!urlWillDropSecrets(server, form)) return [];
  return form.headers.filter((p) => p.stored && p.value === "").map((p) => p.name.trim());
}

/**
 * updateInput is what saving a changed server sends. A pair keeps its stored
 * value unless a new one is typed, except for the headers of a server whose
 * URL changes, which go to the new URL only as typed.
 */
export function updateInput(server: MCPServer, form: MCPFormState): UpdateMCPServer {
  const name = form.name.trim();
  const out: UpdateMCPServer = name === server.name ? {} : { name };
  if (form.kind === "stdio") {
    return {
      ...out,
      command: form.command.trim(),
      args: parseArgs(form.args),
      env: keptValues(form.env),
    };
  }
  const url = form.url.trim();
  const clientId = form.clientId.trim();
  const secret = form.clientSecret.trim();
  const dropped = new Set(droppedHeaders(server, form));
  return {
    ...out,
    ...(url === server.url ? {} : { url }),
    headers: keptValues(form.headers.filter((p) => !dropped.has(p.name.trim()))),
    ...(clientId === server.oauth_client_id ? {} : { oauth_client_id: clientId }),
    ...(secret !== ""
      ? { oauth_client_secret: secret }
      : form.removeSecret
        ? { oauth_client_secret: "" }
        : {}),
  };
}
