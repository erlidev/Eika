/** Wire types for model requests: the next one, and the model calls recorded for a session. */

import type { ReasoningEffort } from "@/api/types/providers";

/** Sampling is the set of parameters sent to the model. */
export type Sampling = {
  temperature?: number;
  top_p?: number;
  top_k?: number;
  min_p?: number;
  frequency_penalty?: number;
  presence_penalty?: number;
  seed?: number;
  stop?: string[];
  max_output?: number;
  reasoning_effort?: ReasoningEffort;
};
import type { ThinkingSwitch } from "@/api/types/providers";
import type { Message } from "@/api/types/sessions";

/** SectionKind names a part of the system prompt. */
export type SectionKind = "base" | "instructions" | "preamble";

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
  /** queue_ms is the wait for a lease on the pool, and slot the queue's slot that served the call; both are null for a call recorded before the queue. class, priority and agent are absent when unknown. */
  queue_ms: number | null;
  slot: number | null;
  class?: string;
  priority?: string;
  agent?: string;
  /** prefill_ms and decode_ms are the time spent reading the prompt and generating; null when nothing measured them. */
  prefill_ms: number | null;
  decode_ms: number | null;
  created_at: string;
};

/** ReasoningSource says where a call's reasoning count came from. */
export type ReasoningSource = "reported" | "tokenised";

/** Prediction is how the token counter reached its expectation of a prompt. */
export type Prediction = "exact" | "anchored" | "estimated";

/**
 * PartSize is the size of one part of a request. approximate is set when the
 * number is not the endpoint's own tokenisation of the part: estimated from
 * bytes, a picture's cost assumed, or a tool sized as JSON text.
 */
export type PartSize = { tokens: number; approximate?: boolean };

/** SectionSize is the size of one section of the system prompt. */
export type SectionSize = PartSize & { kind: SectionKind };

/** ToolSize is the size of one tool definition. */
export type ToolSize = PartSize & { name: string };

/** MessageSize is the size of one message; reasoning_tokens is replayed reasoning, counted apart. */
export type MessageSize = PartSize & { reasoning_tokens?: number };

/** PromptSource says what a breakdown's prompt count is: the endpoint's count of a call, or an exact prediction. */
export type PromptSource = "measured" | "predicted";

/**
 * Breakdown is where a request's tokens go, part by part, each counted on its
 * own and none scaled to fit. The lists follow the order of the request's
 * sections, tools and messages. remainder is prompt_tokens less parts: what the
 * template and the formatting cost. It is negative when the parts add up to
 * more than the endpoint counted, which says a part was counted wrongly.
 */
export type Breakdown = {
  sections: SectionSize[];
  tools: ToolSize[];
  messages: MessageSize[];
  /** parts is the sum of every part, replayed reasoning included. */
  parts: number;
  /** prompt_tokens is the whole request the parts fall short of; null when there is none, which is not 0. */
  prompt_tokens: number | null;
  prompt_source?: PromptSource;
  remainder: number | null;
};

/** PredictedSize is what the token counter expects a request to cost and how it knows. */
export type PredictedSize = { tokens: number; method: Prediction };

/** NextPrediction is the body of GET /api/sessions/{id}/prediction. */
export type NextPrediction = PredictedSize & {
  /** model is the name of the model the prediction is for. */
  model: string;
  /** context_window is the model's effective window. */
  context_window: number;
};

/**
 * ModelContext is one model request by section: the next one
 * (GET /api/sessions/{id}/context) or a recorded one
 * (GET /api/sessions/{id}/calls/{call_id}). The token counts on sections,
 * tools and messages are the agent's planning estimates, four bytes to a
 * token; `breakdown` is what to show.
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
  sources: Record<string, string> | null;
  dropped_effort?: string;
  /** call is the record this is; absent for the next request. */
  call?: ModelCall;
  /** context_window is the model's window in tokens; 0 when the model is not known. */
  context_window: number;
  /** breakdown is where the request's tokens go. */
  breakdown: Breakdown;
  /** prediction is what the counter expects the next request to cost; absent for a recorded call, whose own is on call. */
  prediction?: PredictedSize;
};
