/**
 * The rules the prompt editor shares with the context inspector, as pure
 * functions: how a value that falls through names where it comes from, and
 * how a prompt differs from the built-in one.
 */

import type { ConfigLayer } from "@/api/types";

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
 * promptChange compares a prompt with the built-in one it replaces: how many
 * of its lines the built-in text lacks, and how many of the built-in lines
 * it lacks.
 */
export function promptChange(builtin: string, text: string): PromptChange {
  const before = builtin.split("\n");
  const after = text.split("\n");
  const added = after.filter((line) => !before.includes(line)).length;
  const removed = before.filter((line) => !after.includes(line)).length;
  return { same: builtin === text, added, removed };
}
