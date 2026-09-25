/**
 * The tool choice of a chat, as pure functions: which tools a chat may offer,
 * what turning one on or off sends, and the one line each is described by.
 */

import type { Tool } from "@/api/types";

/** ChatTools splits the harness's tools by whether a chat can offer them. */
export type ChatTools = {
  /** offered are the tools that need no workspace: the ones a chat chooses from. */
  offered: Tool[];
  /** unavailable are the names of the tools a chat never has, sorted. */
  unavailable: string[];
};

/** chatTools splits the tool list for a session with no workspace. */
export function chatTools(tools: Tool[]): ChatTools {
  return {
    offered: tools.filter((t) => !t.needs_workspace),
    unavailable: tools
      .filter((t) => t.needs_workspace)
      .map((t) => t.name)
      .sort(),
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
