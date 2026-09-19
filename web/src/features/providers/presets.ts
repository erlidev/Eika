/**
 * The endpoints the provider form offers as a starting point. Every one of
 * them speaks the OpenAI Chat Completions protocol, which is the one kind the
 * harness implements; a preset only fills in the form.
 */

/** ProviderPreset is one well-known OpenAI-compatible endpoint. */
export type ProviderPreset = {
  /** id is stable and never shown. */
  id: string;
  /** name is what a provider made from it is called. */
  name: string;
  /** blurb says what it is, in a few words. */
  blurb: string;
  baseUrl: string;
  /** keyRequired is false for a local server that asks for no key. */
  keyRequired: boolean;
  /** keyUrl is where the user makes a key. */
  keyUrl?: string;
  /** note is shown under the form when it has something the user must know. */
  note?: string;
};

/** customPresetId is the preset that fills in nothing. */
export const customPresetId = "custom";

/** presets are offered in this order. */
export const presets: readonly ProviderPreset[] = [
  {
    id: "openai",
    name: "OpenAI",
    blurb: "GPT and o-series models",
    baseUrl: "https://api.openai.com/v1",
    keyRequired: true,
    keyUrl: "https://platform.openai.com/api-keys",
  },
  {
    id: "anthropic",
    name: "Anthropic",
    blurb: "Claude, through its OpenAI-compatible API",
    baseUrl: "https://api.anthropic.com/v1/",
    keyRequired: true,
    keyUrl: "https://console.anthropic.com/settings/keys",
    note: "If the model list does not load, add models by name, such as claude-sonnet-4-5.",
  },
  {
    id: "gemini",
    name: "Google Gemini",
    blurb: "Gemini models from Google AI Studio",
    baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai/",
    keyRequired: true,
    keyUrl: "https://aistudio.google.com/apikey",
  },
  {
    id: "openrouter",
    name: "OpenRouter",
    blurb: "Hundreds of models behind one key",
    baseUrl: "https://openrouter.ai/api/v1",
    keyRequired: true,
    keyUrl: "https://openrouter.ai/keys",
  },
  {
    id: "deepseek",
    name: "DeepSeek",
    blurb: "DeepSeek chat and reasoning models",
    baseUrl: "https://api.deepseek.com/v1",
    keyRequired: true,
    keyUrl: "https://platform.deepseek.com/api_keys",
  },
  {
    id: "groq",
    name: "Groq",
    blurb: "Open models on fast inference hardware",
    baseUrl: "https://api.groq.com/openai/v1",
    keyRequired: true,
    keyUrl: "https://console.groq.com/keys",
  },
  {
    id: "mistral",
    name: "Mistral",
    blurb: "Mistral and Codestral models",
    baseUrl: "https://api.mistral.ai/v1",
    keyRequired: true,
    keyUrl: "https://console.mistral.ai/api-keys",
  },
  {
    id: "ollama",
    name: "Ollama",
    blurb: "Local models on this machine",
    baseUrl: "http://host.docker.internal:11434/v1",
    keyRequired: false,
    note: "The harness runs in a container, so it reaches Ollama on the Docker host as host.docker.internal. Ollama must listen on more than 127.0.0.1: start it with OLLAMA_HOST=0.0.0.0.",
  },
  {
    id: "lmstudio",
    name: "LM Studio",
    blurb: "Local models from LM Studio's server",
    baseUrl: "http://host.docker.internal:1234/v1",
    keyRequired: false,
    note: 'Turn on LM Studio\'s server and its "Serve on local network" option, so the harness container can reach it.',
  },
  {
    id: customPresetId,
    name: "Other",
    blurb: "vLLM, llama.cpp, LiteLLM, or any compatible API",
    baseUrl: "",
    keyRequired: false,
    note: "Any server that implements the OpenAI Chat Completions API works. The base URL usually ends in /v1.",
  },
];

/** presetOf finds the preset a stored provider was most likely made from. */
export function presetOf(baseUrl: string): ProviderPreset | undefined {
  const normal = (url: string) => url.trim().replace(/\/+$/, "").toLowerCase();
  return presets.find((p) => p.baseUrl !== "" && normal(p.baseUrl) === normal(baseUrl));
}

/** uniqueProviderName returns `name`, or `name 2`, `name 3`… when it is taken. */
export function uniqueProviderName(name: string, taken: readonly string[]): string {
  if (!taken.includes(name)) return name;
  for (let n = 2; ; n++) {
    const candidate = `${name} ${String(n)}`;
    if (!taken.includes(candidate)) return candidate;
  }
}
