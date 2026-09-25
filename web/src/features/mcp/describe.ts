/**
 * How the MCP settings word a server: its state, what a tool claims of
 * itself, how it was reached, and which of its tools are on.
 */

import type { MCPConnection, MCPServer, MCPServerState, MCPToolAnnotations } from "@/api/types";

/** stateLabels are the words a server's state is shown as. */
export const stateLabels: Record<MCPServerState, string> = {
  disabled: "off",
  idle: "not connected",
  connecting: "connecting",
  connected: "connected",
  unauthorized: "needs sign-in",
  error: "error",
};

/** stateTone is how a state is coloured, from the theme's tokens. */
export const stateTone: Record<MCPServerState, string> = {
  disabled: "text-muted-foreground",
  idle: "text-muted-foreground",
  connecting: "border-warning/40 text-warning",
  connected: "border-success/40 text-success",
  unauthorized: "border-warning/40 text-warning",
  error: "border-destructive/40 text-destructive",
};

/**
 * toolFlags are the server's claims about a tool, as short labels. They are
 * hints the server gives, not guarantees, and a claim left unsaid is not
 * shown. A read-only tool is not also called non-destructive.
 */
export function toolFlags(annotations: MCPToolAnnotations | undefined): string[] {
  if (annotations === undefined) return [];
  const flags: string[] = [];
  if (annotations.read_only === true) flags.push("read-only");
  else if (annotations.destructive === true) flags.push("destructive");
  if (annotations.idempotent === true) flags.push("idempotent");
  if (annotations.open_world === true) flags.push("open world");
  return flags;
}

/** transportLabels name how a connection reached its server. */
export const transportLabels: Record<MCPConnection["transport"], string> = {
  streamable_http: "Streamable HTTP",
  sse: "HTTP+SSE (deprecated)",
  stdio: "stdio",
};

/** eraText says which revision of the protocol a connection speaks. */
export function eraText(connection: MCPConnection): string {
  return connection.era === "modern"
    ? `${connection.protocol_version}, stateless requests`
    : `${connection.protocol_version}, initialize-based`;
}

/** withDisabled is the disabled_tools list after turning one tool on or off: sorted, each once. */
export function withDisabled(server: MCPServer, tool: string, on: boolean): string[] {
  const rest = (server.disabled_tools ?? []).filter((t) => t !== tool);
  return (on ? rest : [...rest, tool]).sort();
}

/** location is the one line a server is reached by: its URL, or its command line. */
export function location(server: MCPServer): string {
  if (server.kind === "http") return server.url;
  return [server.command, ...(server.args ?? [])]
    .map((part) => (/\s/.test(part) ? `"${part}"` : part))
    .join(" ");
}
