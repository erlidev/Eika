/** Wire types for providers and their models. */

/**
 * ReasoningEffort is the Chat Completions reasoning_effort value; "" leaves it
 * to the endpoint. Compatible endpoints disagree on the vocabulary, so it is
 * any word of letters, digits, hyphens, and underscores the user configured,
 * bounded by `maxReasoningEffortLength`.
 */
export type ReasoningEffort = string;

/** maxReasoningEffortLength is the longest reasoning_effort the harness stores. */
export const maxReasoningEffortLength = 32;

/** effortNone is the reasoning effort that turns thinking off. */
export const effortNone = "none";

/**
 * ThinkingSwitch is the request field that carries the effort "none". The
 * standard reasoning_effort is what most endpoints take; chat_template_kwargs
 * and thinking are extensions some need instead, and only one is ever sent.
 */
export type ThinkingSwitch = "reasoning_effort" | "chat_template_kwargs" | "thinking";

/** Model is one model the user configured on a provider. */
export type Model = {
  id: string;
  provider_id: string;
  /** name is what Eika calls the model, unique across providers. */
  name: string;
  /** model is the identifier the provider's endpoint knows it by. */
  model: string;
  context_window: number;
  max_output: number;
  reasoning_effort?: ReasoningEffort;
  /** reasoning_efforts are the values this model offers, in cycling order. */
  reasoning_efforts: ReasoningEffort[];
  /** thinking_switch is the field that turns thinking off when the effort is "none". */
  thinking_switch: ThinkingSwitch;
  preserve_thinking: boolean;
  created_at: string;
  updated_at: string;
};

/** Models is the body of GET /api/models. */
export type Models = {
  models: Model[];
  /** default is the model a run uses when it names none; absent when there are no models. */
  default?: string;
};

/** CreateModel is the body of POST /api/models. */
export type CreateModel = {
  provider_id: string;
  /** name defaults to model. */
  name?: string;
  model: string;
  context_window: number;
  max_output: number;
  reasoning_effort?: ReasoningEffort;
  reasoning_efforts?: ReasoningEffort[];
  thinking_switch?: ThinkingSwitch;
  preserve_thinking?: boolean;
};

/** UpdateModel is the body of PATCH /api/models/{id}; an absent field is left alone. */
export type UpdateModel = Partial<Omit<CreateModel, "provider_id">>;

/** TestModel is the body of POST /api/models/test. */
export type TestModel = {
  provider_id: string;
  model: string;
  reasoning_effort?: ReasoningEffort;
  thinking_switch?: ThinkingSwitch;
  preserve_thinking?: boolean;
};

/** TestModelResult is what one small request to a model came back with. */
export type TestModelResult = {
  reply: string;
  stop_reason: string;
  latency_ms: number;
};

/** Provider is one model provider: an endpoint and whether it holds a key. */
export type Provider = {
  id: string;
  name: string;
  kind: string;
  base_url: string;
  api_key_set: boolean;
  /** api_key_hint is the last characters of a long key. */
  api_key_hint?: string;
  created_at: string;
  updated_at: string;
};

/** Providers is the body of GET /api/providers. */
export type Providers = {
  providers: Provider[];
  kinds: string[];
};

/** CreateProvider is the body of POST /api/providers. */
export type CreateProvider = {
  name: string;
  kind?: string;
  base_url: string;
  api_key?: string;
};

/** UpdateProvider is the body of PATCH /api/providers/{id}; an absent field is left alone. */
export type UpdateProvider = {
  name?: string;
  base_url?: string;
  /** api_key replaces the stored key; "" removes it. */
  api_key?: string;
};

/**
 * ProbeProvider is the body of POST /api/providers/probe: a stored provider,
 * an endpoint not saved yet, or a stored one with fields the form changed.
 */
export type ProbeProvider = {
  provider_id?: string;
  kind?: string;
  base_url?: string;
  api_key?: string;
};

/** ModelInfo is one model an endpoint reports; limits are 0 or absent when it does not say. */
export type ModelInfo = {
  id: string;
  context_window?: number;
  max_output?: number;
};
