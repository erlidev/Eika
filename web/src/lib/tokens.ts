/**
 * The token estimate the harness uses before a model has measured anything:
 * internal/agent's EstimateTokens, four bytes of UTF-8 to a token. An editor
 * uses it to say what a draft costs before it is saved; every size the
 * harness reports is the same estimate, so the two agree.
 */

const bytesPerToken = 4;

const encoder = new TextEncoder();

/** estimateTokens estimates how many tokens text costs. */
export function estimateTokens(text: string): number {
  return Math.ceil(encoder.encode(text).length / bytesPerToken);
}
