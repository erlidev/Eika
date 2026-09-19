/**
 * Starting values for a model's limits. An endpoint that reports a model's
 * context size wins; otherwise a short table of well-known model families
 * gives a value the user can correct. The harness refuses a request that
 * cannot fit the context window, and an endpoint refuses a max_output larger
 * than the model allows, so a conservative guess is the better mistake.
 */

import type { ModelInfo } from "@/api/types";

/** Limits are a model's context window and output limit in tokens. */
export type Limits = { context_window: number; max_output: number };

/** fallback is what an unknown model gets. */
const fallback: Limits = { context_window: 128_000, max_output: 16_384 };

/** families maps a model id, without a vendor prefix, to its usual limits. */
const families: readonly (readonly [RegExp, Limits])[] = [
  [/^gpt-5/, { context_window: 400_000, max_output: 128_000 }],
  [/^gpt-4\.1/, { context_window: 1_047_576, max_output: 32_768 }],
  [/^gpt-4o/, { context_window: 128_000, max_output: 16_384 }],
  [/^o[134](-|$)/, { context_window: 200_000, max_output: 100_000 }],
  [/^claude-(opus|sonnet|haiku)-4|^claude-.*-4/, { context_window: 200_000, max_output: 32_000 }],
  [/^claude/, { context_window: 200_000, max_output: 8_192 }],
  [/^gemini-(2\.5|3)/, { context_window: 1_048_576, max_output: 65_536 }],
  [/^gemini/, { context_window: 1_048_576, max_output: 8_192 }],
  [/^deepseek/, { context_window: 128_000, max_output: 8_192 }],
  [
    /^(qwen|llama|mistral|mixtral|codestral|gemma|phi)/,
    { context_window: 32_768, max_output: 8_192 },
  ],
];

/** suggestLimits returns the limits a new model starts with. */
export function suggestLimits(id: string, reported?: ModelInfo): Limits {
  const base = id.toLowerCase().split("/").pop() ?? "";
  const known = families.find(([pattern]) => pattern.test(base))?.[1] ?? fallback;
  const window = positive(reported?.context_window) ?? known.context_window;
  const output =
    positive(reported?.max_output) ??
    (positive(reported?.context_window) !== undefined
      ? Math.min(known.max_output, Math.floor(window / 4))
      : known.max_output);
  return { context_window: window, max_output: Math.min(output, window) };
}

/** positive returns n when it is a positive number. */
function positive(n: number | undefined): number | undefined {
  return n !== undefined && Number.isFinite(n) && n > 0 ? Math.floor(n) : undefined;
}

/**
 * suggestModelName returns the name a new model gets: its id, or the id under
 * the provider's name when another model already has that name. Names are
 * unique across providers because a run and a subagent name a model alone.
 */
export function suggestModelName(
  id: string,
  providerName: string,
  taken: readonly string[],
): string {
  if (!taken.includes(id)) return id;
  const slug = providerName
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
  const scoped = `${slug === "" ? "provider" : slug}/${id}`;
  if (!taken.includes(scoped)) return scoped;
  for (let n = 2; ; n++) {
    const candidate = `${scoped}-${String(n)}`;
    if (!taken.includes(candidate)) return candidate;
  }
}
