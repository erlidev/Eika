/**
 * The tool choice of a chat, as pure functions: which tools a chat may offer,
 * what turning one on or off sends, and the one line each is described by.
 */

import type { Tool } from "@/api/types";

/** ServerTools are the tools one MCP server offers a chat. */
export type ServerTools = { server: string; tools: Tool[] };

/** ChatTools splits the harness's tools by whether a chat can offer them. */
export type ChatTools = {
  /** offered are the built-in tools that need no workspace: the ones a chat chooses from. */
  offered: Tool[];
  /** servers are the MCP servers whose tools a chat can offer, by name, each with its tools. */
  servers: ServerTools[];
  /** unavailable are the names of the built-in tools a chat never has, sorted. */
  unavailable: string[];
  /** unavailableServers are the MCP servers that run in a workspace, which a chat has none of. */
  unavailableServers: string[];
};

/** chatTools splits the tool list for a session with no workspace. */
export function chatTools(tools: Tool[]): ChatTools {
  const servers = new Map<string, Tool[]>();
  const unavailableServers = new Set<string>();
  const offered: Tool[] = [];
  const unavailable: string[] = [];
  for (const t of tools) {
    if (t.server === undefined) {
      if (t.needs_workspace) unavailable.push(t.name);
      else offered.push(t);
    } else if (t.needs_workspace) {
      unavailableServers.add(t.server);
    } else {
      servers.set(t.server, [...(servers.get(t.server) ?? []), t]);
    }
  }
  return {
    offered,
    servers: [...servers]
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
      .map(([server, list]) => ({ server, tools: list })),
    unavailable: unavailable.sort(),
    unavailableServers: [...unavailableServers].sort(),
  };
}

/**
 * withTool is the tool list to send after turning one tool on or off:
 * sorted, each name once, as the harness stores it.
 */
export function withTool(current: readonly string[], name: string, on: boolean): string[] {
  const rest = current.filter((t) => t !== name);
  return (on ? [...rest, name] : rest).sort();
}

/**
 * toolSummary is the first sentence of a tool's description. The description
 * is written for the model and runs to several lines; a person choosing the
 * tool needs the one that says what it is.
 */
export function toolSummary(description: string): string {
  const line = description.split("\n", 1)[0]?.trim() ?? "";
  const end = line.search(/\.(\s|$)/);
  return end === -1 ? line : line.slice(0, end + 1);
}
