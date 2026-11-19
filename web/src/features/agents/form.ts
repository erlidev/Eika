/**
 * The rules the prompt editor shares with the context inspector, as pure
 * functions: how a value that falls through names where it comes from, and
 * how a prompt differs from the built-in one.
 */

import { lineDiff } from "@/lib/lineDiff";

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
