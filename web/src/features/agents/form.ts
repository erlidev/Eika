/**
 * The rules the prompt editor shares with the context inspector, as pure
 * functions: how a value that falls through names where it comes from, and
 * how a prompt differs from the built-in one.
 */

import type { ConfigLayer } from "@/api/types";
import { lineDiff } from "@/lib/lineDiff";

/** layerNames say where a value falls through to, as the editor words it. */
const layerNames: Record<ConfigLayer, string> = {
  agent: "the agent",
  class: "the model class",
  request: "the message",
  session: "this session",
  profile: "the profile",
  model: "the model",
  default: "the default",
};

/** layerName is how the editor names a layer: "the agent", "the model". */
export function layerName(layer: ConfigLayer): string {
  return layerNames[layer];
}

/** PromptChange is how a prompt differs from the built-in one, line by line. */
export type PromptChange = { same: boolean; added: number; removed: number };

/**
 * promptChange compares a prompt with the built-in one it replaces, from the
 * same line diff the editor shows: how many lines it adds and how many it
 * drops. A moved or repeated line counts as the change the diff draws.
 */
export function promptChange(builtin: string, text: string): PromptChange {
  const rows = lineDiff(builtin, text);
  return {
    same: builtin === text,
    added: rows.filter((r) => r.kind === "add").length,
    removed: rows.filter((r) => r.kind === "remove").length,
  };
}
