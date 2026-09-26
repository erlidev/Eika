/**
 * Tools as a chat sees them, as pure functions: what a chat can never offer,
 * and the one line each tool is described by.
 */

import type { Tool } from "@/api/types";

/** OutOfChat names what a chat can never offer, having no workspace. */
export type OutOfChat = {
  /** tools are the built-in tools that need a workspace, sorted. */
  tools: string[];
  /** servers are the MCP servers that run as a command in a workspace, sorted. */
  servers: string[];
};

/**
 * outOfChat lists the tools a chat can never offer: the built-in ones that
 * need a workspace, and the servers that run in one. The ones it can offer
 * are chosen with the ToolPicker, whose groups leave these out.
 */
export function outOfChat(tools: readonly Tool[]): OutOfChat {
  const names: string[] = [];
  const servers = new Set<string>();
  for (const t of tools) {
    if (!t.needs_workspace) continue;
    if (t.server === undefined) names.push(t.name);
    else servers.add(t.server);
  }
  return { tools: names.sort(), servers: [...servers].sort() };
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
