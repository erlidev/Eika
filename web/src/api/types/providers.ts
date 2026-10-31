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
  /** probed_window is the context one request may use on the endpoint, as its last probe read it; absent when it states none. */
  probed_window?: number;
  /** effective_window is the smaller of context_window and probed_window: what runs are judged against. */
  effective_window: number;
  max_output: number;
  reasoning_effort?: ReasoningEffort;
  /** reasoning_efforts are the values this model offers, in cycling order. */
  reasoning_efforts: ReasoningEffort[];
  /** thinking_switch is the field that turns thinking off when the effort is "none". */
  thinking_switch: ThinkingSwitch;
  preserve_thinking: boolean;
  /** image_input says the model reads images; a request sends it the conversation's images. */
  image_input: boolean;
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
  image_input?: boolean;
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
  /** image_input sends a small picture with the question, proving the endpoint takes images. */
  image_input?: boolean;
};

/** TestModelResult is what one small request to a model came back with. */
export type TestModelResult = {
  reply: string;
  stop_reason: string;
  latency_ms: number;
};

/**
 * Backend is the kind of server behind an endpoint: a probe detects it, and
 * the user can override it. `openai` is a hosted API; `other` is a server on
 * the local network nothing recognised.
 */
export type Backend = "llamacpp" | "vllm" | "sglang" | "tabby" | "openai" | "other";

/** backends lists every Backend, in the order a menu offers them. */
export const backends: readonly Backend[] = [
  "llamacpp",
  "vllm",
  "sglang",
  "tabby",
  "openai",
  "other",
];

/**
 * KV says how an endpoint's KV cache is divided among its requests: one cache
 * all of them draw on, a fixed share per slot, or not known (the server does
 * not say and the probe cannot tell, as with llama.cpp and several slots).
 */
export type KV = "shared" | "partitioned" | "unknown";

/** kvs lists every KV, in the order a menu offers them. */
export const kvs: readonly KV[] = ["shared", "partitioned", "unknown"];

/** Capabilities is what the last successful probe found beyond the settings. */
export type Capabilities = {
  /** build is the version the server reported, such as llama.cpp's b11942-338fce651. */
  build?: string;
  /** window is the context, in tokens, one request may use, as the server states it. */
  window?: number;
  /** counting is how the endpoint counts tokens: a rendered chat request, plain text, or not at all. */
  counting: "chat" | "text" | "none";
  /** cache_metrics says whether the endpoint reports cached prompt tokens. */
  cache_metrics: "reported" | "unknown";
  /** slot_hints is whether a request may name the slot it wants. */
  slot_hints: boolean;
};

/** ProviderField names a probed field the user can set in place of the probe. */
export type EndpointField = "backend" | "pool" | "slots" | "kv";

/** Provider is one model provider: an endpoint and whether it holds a key. */
export type Provider = {
  id: string;
  name: string;
  kind: string;
  base_url: string;
  api_key_set: boolean;
  /** api_key_hint is the last characters of a long key. */
  api_key_hint?: string;
  backend: Backend;
  /** pool is the hardware group: endpoints in one pool share hardware. It is the provider's name unless set. */
  pool: string;
  /** slots is the concurrent requests the endpoint takes. */
  slots: number;
  kv: KV;
  /** kv_tokens is the size of a shared cache in tokens; absent when unknown. */
  kv_tokens?: number;
  capabilities: Capabilities;
  /** overridden names the fields the user set, which a probe leaves alone. */
  overridden: EndpointField[];
  /** probed_at is when a probe last succeeded; absent if none has. */
  probed_at?: string;
  /** probe_error is why the latest probe failed; absent after a success. */
  probe_error?: string;
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
  /** backend, pool, slots and kv set what a probe would otherwise find; a probe leaves them alone. */
  backend?: Backend;
  /** pool "" means the provider's name. */
  pool?: string;
  slots?: number;
  kv?: KV;
  /** kv_tokens is the size of a shared cache and is set with kv. */
  kv_tokens?: number;
  /** reset hands fields back to the probe; their values stay until the next probe. */
  reset?: EndpointField[];
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
  /** image_input is true when the endpoint says the model reads images. */
  image_input?: boolean;
};

/** ProbeFindings is what a probe learned about an endpoint, before any override. */
export type ProbeFindings = {
  backend: Backend;
  slots: number;
  kv: KV;
  kv_tokens?: number;
  capabilities: Capabilities;
};

/**
 * ProbeResponse is the body of POST /api/providers/probe: the models, what
 * the probe found, and, when the probe was of a stored provider's own
 * endpoint, that provider as it now stands with the findings recorded.
 */
export type ProbeResponse = {
  models: ModelInfo[];
  probe: ProbeFindings;
  provider?: Provider;
};
