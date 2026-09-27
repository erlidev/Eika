/** Wire types for runs, their questions, and posted messages. */

import type { Elicitation } from "@/api/types/mcp";

/** RunState is where an agent run ended up. */
export type RunState = "running" | "done" | "error" | "aborted";

/** Run is one execution of the agent loop on a session. */
export type Run = {
  id: string;
  session_id: string;
  state: RunState;
  started_at: string;
  finished_at?: string;
  error?: string;
};

/** Question is an `ask_user` call a run is blocked on. */
export type Question = {
  id: string;
  session_id: string;
  run_id: string;
  call_id: string;
  question: string;
  options?: string[];
  allow_free_text: boolean;
  asked_at: string;
};

/** QueuedMessage is a message waiting in a run's queue. */
export type QueuedMessage = {
  text: string;
  /** images is how many images the message carries. */
  images: number;
};

/** RunStatus is the body of GET /api/sessions/{id}/run. */
export type RunStatus = {
  session_id: string;
  active: boolean;
  run?: Run;
  pending_steering: QueuedMessage[];
  pending_follow_ups: QueuedMessage[];
  questions: Question[];
  /** elicitations are what MCP servers asked the user during this session's tool calls. */
  elicitations: Elicitation[];
};

/** MessageMode says what the harness does with a posted message. */
export type MessageMode = "run" | "steer" | "follow_up";

/** CompactRequest is the body of POST /api/sessions/{id}/compact. */
export type CompactRequest = {
  /** instructions say what the summary should focus on. */
  instructions?: string;
  /** model writes the summary; absent uses the one a run would. */
  model?: string;
};

/** PostImage is one image attached to a posted message: the base64 file. */
export type PostImage = {
  data: string;
};

/** PostMessage is the body of POST /api/sessions/{id}/messages. */
export type PostMessage = {
  /** text may be empty when images carries at least one image. */
  text: string;
  images?: PostImage[];
  mode?: MessageMode;
  model?: string;
};
