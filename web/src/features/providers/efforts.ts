/**
 * The rules behind a model's reasoning efforts. Compatible endpoints disagree
 * on the vocabulary — OpenAI takes minimal through high, others take none,
 * xhigh, max, or a word of their own — so the choices belong to the model the
 * user configured rather than to a list in the code. These are pure, so they
 * are tested here and mirror what the harness refuses.
 */

import { maxReasoningEffortLength } from "@/api/types";
import type { ReasoningEffort } from "@/api/types";

/** maxReasoningEfforts is how many choices one model may offer. */
export const maxReasoningEfforts = 12;

/**
 * commonEfforts are offered as suggestions when a list is being built. They
 * are the words today's endpoints use; anything else is typed in.
 */
export const commonEfforts: readonly string[] = [
  "none",
  "minimal",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
];

/**
 * effortProblem says why an effort cannot be added to a model's list, or null
 * when it can. It mirrors the harness's own check so the form says why the
 * button waits instead of the endpoint saying it later.
 */
export function effortProblem(raw: string, existing: readonly string[]): string | null {
  const effort = raw.trim();
  if (effort === "") return "Type the word the endpoint expects, such as high.";
  if (effort.length > maxReasoningEffortLength) {
    return `Shorten it to ${String(maxReasoningEffortLength)} characters or fewer.`;
  }
  if (!/^[A-Za-z0-9_-]+$/.test(effort)) {
    return "Use letters, digits, hyphens, and underscores only.";
  }
  if (existing.includes(effort)) return `“${effort}” is already on the list.`;
  if (existing.length >= maxReasoningEfforts) {
    return `A model offers at most ${String(maxReasoningEfforts)} efforts.`;
  }
  return null;
}

/**
 * nextEffort is the effort after the current one, cycling back to the start.
 * An effort that is not on the list, which a list the user just changed can
 * leave behind, starts the cycle over.
 */
export function nextEffort(
  current: ReasoningEffort,
  efforts: readonly ReasoningEffort[],
): ReasoningEffort {
  if (efforts.length === 0) return current;
  const index = efforts.indexOf(current);
  return efforts[(index + 1) % efforts.length] ?? current;
}
