/** Wire types for profiles and the configuration layers they resolve. */

import type { ReasoningEffort } from "@/api/types/providers";

/**
 * Sampling holds the parameters that steer how a model samples. A parameter
 * left out is not set at that layer: for what is sent, the endpoint's default.
 * An empty stop list sends none, which clears the list of a layer below.
 */
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

/** SamplingKey names one sampling parameter. */
export type SamplingKey = keyof Sampling;

/**
 * ConfigLayer is where a resolved value came from, top first: the run
 * request, the session's overrides, its profile, the model row, or the
 * defaults (the default model and profile, the built-in prompts, every tool,
 * and for a sampling parameter the endpoint's own).
 */
export type ConfigLayer = "request" | "session" | "profile" | "model" | "default";

/**
 * ProfileSettings is what a profile sets, and what a session overrides of
 * it. null, an absent model_id, and a sampling parameter left out are not
 * set, and fall through.
 */
export type ProfileSettings = {
  model_id?: string;
  /** workspace_prompt and chat_prompt replace the built-in base prompts, "" included. */
  workspace_prompt: string | null;
  chat_prompt: string | null;
  instructions: string | null;
  context_files: boolean | null;
  /** preserve_thinking replays earlier reasoning to the model, over the model's own switch. */
  preserve_thinking: boolean | null;
  sampling: Sampling;
};

/** Configuration is what a run resolves to, with the layer each value came from. */
export type Configuration = {
  profile_id: string;
  profile_name: string;
  /** model_id and model are empty when no model is configured. */
  model_id: string;
  model: string;
  workspace_prompt: string;
  chat_prompt: string;
  instructions: string;
  context_files: boolean;
  preserve_thinking: boolean;
  /** tools is the tool choice; null is every tool the session can run. */
  tools: string[] | null;
  sampling: Sampling;
  /** dropped_effort is an effort chosen above the model that the model does not offer. */
  dropped_effort?: string;
  /**
   * sources names the layer of each value: profile, model, workspace_prompt,
   * chat_prompt, instructions, context_files, preserve_thinking, tools, and
   * `sampling.<parameter>` for each parameter sent.
   */
  sources: Record<string, ConfigLayer> | null;
};

/** Profile is a named configuration of what a run sends. */
export type Profile = ProfileSettings & {
  id: string;
  name: string;
  description: string;
  /** tools is the tool choice, where `mcp__<server>__*` is every tool of a server; null is every tool. */
  tools: string[] | null;
  /** inherited is what the profile's unset values fall through to. */
  inherited: Configuration;
  created_at: string;
  updated_at: string;
};

/** Profiles is the body of GET /api/profiles. */
export type Profiles = {
  profiles: Profile[] | null;
  /** default is the id of the profile a session that chose none runs with. */
  default: string;
  /** prompts are the built-in base prompts. */
  prompts: { workspace: string; chat: string };
};

/** ProfileInput is the body of POST /api/profiles and PUT /api/profiles/{id}: the whole profile. */
export type ProfileInput = Partial<ProfileSettings> & {
  name: string;
  description?: string;
  tools?: string[] | null;
};

/** SessionConfiguration is what a session sets itself and what its next run resolves to. */
export type SessionConfiguration = {
  session_id: string;
  /** profile_id is the profile the session chose; absent, the default. */
  profile_id?: string;
  overrides: ProfileSettings;
  /** tools is the session's own tool choice, null when it has made none. */
  tools: string[] | null;
  /** resolved is what the next run uses when the message names no model. */
  resolved: Configuration;
  /** inherited is the same as if the session set nothing itself. */
  inherited: Configuration;
};
