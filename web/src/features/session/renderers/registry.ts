/**
 * The tool renderer registry. One entry per built-in tool decides how its
 * call is summarised in the collapsed card and what the expanded card shows.
 * A tool with no entry falls back to `jsonRenderer`, so a new tool renders
 * usefully before anyone writes a renderer for it.
 *
 * Adding a renderer: write the component, add one line to `toolRenderers`.
 * The walkthrough is in docs/EXTENDING.md.
 */

import type { ToolItem } from "@/features/session/transcript";

/** ToolRendererProps is what every tool renderer receives. */
export type ToolRendererProps = {
  /** call is the tool call with everything known about it so far. */
  call: ToolItem;
};

/** ToolRenderer is how one tool's calls are drawn. */
export type ToolRenderer = {
  /**
   * summary is the one line the collapsed card shows next to the tool name:
   * the command, the path, the pattern. Keep it short; it is not wrapped.
   */
  summary: (call: ToolItem) => string;
  /** Body is the expanded card's content. */
  Body: (props: ToolRendererProps) => React.ReactNode;
};

/** args narrows a call's arguments to a record for a renderer to read. */
export function args(call: ToolItem): Record<string, unknown> {
  return typeof call.arguments === "object" && call.arguments !== null
    ? (call.arguments as Record<string, unknown>)
    : {};
}

/** stringArg reads one string argument, empty when it is absent or not one. */
export function stringArg(call: ToolItem, name: string): string {
  const value = args(call)[name];
  return typeof value === "string" ? value : "";
}

/** numberArg reads one numeric argument, undefined when it is absent. */
export function numberArg(call: ToolItem, name: string): number | undefined {
  const value = args(call)[name];
  return typeof value === "number" ? value : undefined;
}

/** detail reads one field of a tool result's `details` object. */
export function detail(call: ToolItem, name: string): unknown {
  if (typeof call.details !== "object" || call.details === null) return undefined;
  return (call.details as Record<string, unknown>)[name];
}
