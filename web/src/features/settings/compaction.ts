/**
 * The rules behind the compaction setting. They are pure, so they are tested
 * here, and they mirror what the harness refuses.
 */

import type { CompactionPrompts, CompactionSettings, SettingsState } from "@/api/types";

/** settingCompaction is the settings key the compaction setting is stored under. */
export const settingCompaction = "compaction";

/** The bounds the harness accepts for either budget, in tokens. */
export const minCompactionTokens = 1024;
export const maxCompactionTokens = 1 << 20;

/** maxPromptBytes bounds one prompt, as it bounds a profile's prompts. */
export const maxPromptBytes = 64 << 10;

/** promptNames are the prompts in the order the form shows them. */
export const promptNames: readonly (keyof CompactionPrompts)[] = [
  "summary",
  "update",
  "turn_prefix",
];

/**
 * compactionSetting is what the settings store over the harness's defaults,
 * with an empty prompt wherever the built-in one is used. The table holds
 * any JSON, so a stored field is read only when it has the right type.
 */
export function compactionSetting(state: SettingsState): CompactionSettings {
  const defaults = state.defaults.compaction;
  const raw = state.settings[settingCompaction];
  const stored = typeof raw === "object" && raw !== null ? (raw as Record<string, unknown>) : {};
  const prompts =
    typeof stored.prompts === "object" && stored.prompts !== null
      ? (stored.prompts as Record<string, unknown>)
      : {};
  const text = (name: keyof CompactionPrompts) => {
    const value = prompts[name];
    return typeof value === "string" ? value : "";
  };
  const count = (value: unknown, fallback: number) =>
    typeof value === "number" && Number.isFinite(value) ? value : fallback;
  return {
    auto: typeof stored.auto === "boolean" ? stored.auto : defaults.auto,
    reserve_tokens: count(stored.reserve_tokens, defaults.reserve_tokens),
    keep_recent_tokens: count(stored.keep_recent_tokens, defaults.keep_recent_tokens),
    prompts: { summary: text("summary"), update: text("update"), turn_prefix: text("turn_prefix") },
  };
}

/** tokensProblem says why a budget cannot be saved, or undefined when it can. */
export function tokensProblem(n: number): string | undefined {
  if (Number.isInteger(n) && n >= minCompactionTokens && n <= maxCompactionTokens) return undefined;
  return `Enter a whole number from ${String(minCompactionTokens)} to ${String(maxCompactionTokens)}.`;
}

/** promptProblem says why a prompt cannot be saved, or undefined when it can. */
export function promptProblem(text: string): string | undefined {
  if (new TextEncoder().encode(text).length <= maxPromptBytes) return undefined;
  return `Shorten it to ${String(maxPromptBytes / 1024)} KiB or less.`;
}

/**
 * savedCompaction is the setting as the form saves it: every prompt that is
 * only whitespace becomes empty, which is the built-in one.
 */
export function savedCompaction(c: CompactionSettings): CompactionSettings {
  const prompt = (text: string) => (text.trim() === "" ? "" : text);
  return {
    ...c,
    prompts: {
      summary: prompt(c.prompts.summary),
      update: prompt(c.prompts.update),
      turn_prefix: prompt(c.prompts.turn_prefix),
    },
  };
}
