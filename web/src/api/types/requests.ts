/** Wire types for model requests: the next one, and the model calls recorded for a session. */

import type { ConfigLayer, Sampling } from "@/api/types/profiles";
import type { ThinkingSwitch } from "@/api/types/providers";
import type { Message } from "@/api/types/sessions";

/** SectionKind names a part of the system prompt. */
export type SectionKind = "base" | "instructions";

/** ContextSection is one part of the system prompt, with its estimated size. */
export type ContextSection = {
  kind: SectionKind;
  text: string;
  tokens: number;
};

/** ToolSchema is one tool definition a request sends. */
export type ToolSchema = {
  name: string;
  description: string;
  schema: unknown;
  source: "builtin" | "mcp";
  tokens: number;
};

/** RequestParameters are what a request sends beside its content. */
export type RequestParameters = {
  model: string;
  sampling: Sampling;
  thinking_switch?: ThinkingSwitch;
  preserve_thinking: boolean;
  /** image_input says the images are sent; without it each message notes they were left out. */
  image_input: boolean;
};

/** ModelCall is the record of one model call, without what it sent. */
export type ModelCall = {
  id: string;
  session_id: string;
  run_id: string;
  entry_id?: string;
  /** model_id is absent once the model is deleted; model is its name at the time. */
  model_id?: string;
  model: string;
  message_tokens: number;
  /**
   * prompt_tokens, completion_tokens, total_tokens, cached_tokens, and
   * prefilled_tokens are what the endpoint measured (the prompt includes the
   * cached part, the completion includes the reasoning); null when it did not
   * state them, which is not 0.
   */
  prompt_tokens: number | null;
  completion_tokens: number | null;
  total_tokens: number | null;
  cached_tokens: number | null;
  prefilled_tokens: number | null;
  /** reasoning_tokens is the reasoning part of the completion; its source says where it came from. */
  reasoning_tokens: number | null;
  /** reasoning_source is `tokenised` when the count is of the reasoning text, an approximation. */
  reasoning_source?: ReasoningSource;
  /** predicted_prompt_tokens is what the token counter expected the prompt to be, and prediction how. */
  predicted_prompt_tokens: number | null;
  prediction?: Prediction;
  /** drift is predicted_prompt_tokens less prompt_tokens; any drift on an exact prediction is a bug. */
  drift: number | null;
  /** context_window is the window the call was judged against; null when the model had none. */
  context_window: number | null;
  /** endpoint is the provider's name when the call was made. */
  endpoint?: string;
  /** queue_ms and slot are null, and class, priority and agent absent, until the inference queue and agents record them. */
  queue_ms: number | null;
  slot: number | null;
  class?: string;
  priority?: string;
  agent?: string;
  created_at: string;
};

/** ReasoningSource says where a call's reasoning count came from. */
export type ReasoningSource = "reported" | "tokenised";

/** Prediction is how the token counter reached its expectation of a prompt. */
export type Prediction = "exact" | "anchored" | "estimated";

/** ModelCalls is the body of GET /api/sessions/{id}/calls. */
export type ModelCalls = {
  session_id: string;
  calls: ModelCall[] | null;
};

/**
 * ModelContext is one model request by section: the next one
 * (GET /api/sessions/{id}/context) or a recorded one
 * (GET /api/sessions/{id}/calls/{call_id}). Token counts are
 * estimates, four bytes to a token.
 */
export type ModelContext = {
  sections: ContextSection[] | null;
  tools: ToolSchema[] | null;
  messages: Message[] | null;
  message_tokens: number;
  /** message_sizes is the estimated size of each message, in order. */
  message_sizes: number[] | null;
  parameters: RequestParameters;
  /** sources names the layer of each parameter: model, thinking_switch, preserve_thinking, sampling.<name>. */
  sources: Record<string, ConfigLayer> | null;
  dropped_effort?: string;
  /** call is the record this is; absent for the next request. */
  call?: ModelCall;
  /** context_window is the model's window in tokens; 0 when the model is not known. */
  context_window: number;
  /** calibration is a measured call to scale the estimates to. */
  calibration?: { call_id: string; prompt_tokens: number; estimated_tokens: number };
};
