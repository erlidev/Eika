/** Wire types for model requests: the next one and the recorded ones. */

import type { ConfigLayer, Sampling } from "@/api/types/profiles";
import type { ThinkingSwitch } from "@/api/types/providers";
import type { Message } from "@/api/types/sessions";

/** SectionKind names a part of the system prompt. */
export type SectionKind = "base" | "context_files" | "instructions";

/** ContextSection is one part of the system prompt, with its estimated size. */
export type ContextSection = {
  kind: SectionKind;
  text: string;
  tokens: number;
  /** files are the context files a context_files section renders. */
  files?: { path: string; text: string; tokens: number }[];
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
};

/** ModelRequest is the record of one model call, without what it sent. */
export type ModelRequest = {
  id: string;
  session_id: string;
  run_id: string;
  entry_id?: string;
  /** model_id is absent once the model is deleted; model is its name at the time. */
  model_id?: string;
  model: string;
  message_tokens: number;
  /** input_tokens, output_tokens, and total_tokens are measured; 0 when not reported. */
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  created_at: string;
};

/** ModelRequests is the body of GET /api/sessions/{id}/requests. */
export type ModelRequests = {
  session_id: string;
  requests: ModelRequest[] | null;
};

/**
 * ModelContext is one model request by section: the next one
 * (GET /api/sessions/{id}/context) or a recorded one
 * (GET /api/sessions/{id}/requests/{request_id}). Token counts are
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
  /**
   * context_files_unread says why a preview has no context files where a
   * run would read them: the workspace is not running.
   */
  context_files_unread?: string;
  /** request is the record this is; absent for the next request. */
  request?: ModelRequest;
  /** context_window is the model's window in tokens; 0 when the model is not known. */
  context_window: number;
  /** calibration is a measured call to scale the estimates to. */
  calibration?: { request_id: string; input_tokens: number; estimated_tokens: number };
};
