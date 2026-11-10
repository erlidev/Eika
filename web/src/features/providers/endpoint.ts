/**
 * What the UI says about an endpoint's hardware: the backend a probe detected,
 * its slots, its per-request window, and how its KV cache is divided. Pure, so
 * the wording is tested here and the components only place it.
 */

import type { API, Backend, Conformance, KV, Provider, UpdateProvider } from "@/api/types";

/** backendLabels are the backends' names as their projects write them. */
export const backendLabels: Record<Backend, string> = {
  llamacpp: "llama.cpp",
  vllm: "vLLM",
  sglang: "SGLang",
  tabby: "TabbyAPI",
  openai: "Hosted API",
  other: "Other server",
};

/** kvLabels name how a cache is divided. */
export const kvLabels: Record<KV, string> = {
  shared: "Shared",
  partitioned: "Partitioned",
  unknown: "Not known",
};

/** apiLabels name the wire protocols. */
export const apiLabels: Record<API, string> = {
  responses: "Responses API",
  chat: "Chat Completions",
};

/**
 * conformanceSummary says what the Responses check found, in a sentence: that
 * it has not run, or which model and build it ran against and how it ended.
 */
export function conformanceSummary(c: Conformance | undefined): string {
  if (c === undefined) return "The Responses API has not been checked yet.";
  const against = [
    c.model !== undefined && c.model !== "" ? `with ${c.model}` : "",
    c.build !== undefined && c.build !== "" ? `on ${c.build}` : "",
  ]
    .filter((part) => part !== "")
    .join(" ");
  const where = against === "" ? "" : ` ${against}`;
  switch (c.verdict) {
    case "passed":
      return `The Responses API passed its check${where}${c.reasoning ? "" : ", which returned no reasoning to send back"}.`;
    case "failed":
      return `The Responses API failed its check${where}: ${c.reason ?? "no reason given"}`;
    case "skipped":
      return `The Responses API was not checked: ${c.reason ?? "no reason given"}`;
  }
}

/** formatCount renders a whole number with thousands separators, such as 131,072. */
export function formatCount(n: number): string {
  return n.toLocaleString("en-US");
}

/**
 * formatProbed renders when a probe last succeeded, in UTC to the minute, so
 * it reads the same on every machine: "2026-10-31 12:00 UTC".
 */
export function formatProbed(iso: string): string {
  return `${iso.slice(0, 10)} ${iso.slice(11, 16)} UTC`;
}

/** the fields of a provider the hardware summary reads. */
type Hardware = Pick<Provider, "backend" | "slots" | "kv" | "kv_tokens" | "capabilities">;

/**
 * kvSummary says how the cache is divided, in tokens where it is shared:
 * "shared 131,072", "shared, size not known", "partitioned" or "not known".
 */
export function kvSummary(kv: KV, tokens?: number): string {
  switch (kv) {
    case "shared":
      return tokens !== undefined && tokens > 0
        ? `shared ${formatCount(tokens)}`
        : "shared, size not known";
    case "partitioned":
      return "partitioned";
    case "unknown":
      return "not known";
  }
}

/**
 * hardwareSummary is the one line a provider card shows under the address:
 * the backend and its build number, the slots, the window one request may use
 * (in tokens), and the cache (in tokens where it is shared). A provider no
 * probe has answered for says so. The build is cut at its first hyphen, so a
 * llama.cpp build reads b11942 and not b11942-338fce651: the card line is one
 * row, and the endpoint settings state the whole build.
 */
export function hardwareSummary(p: Hardware & Pick<Provider, "probed_at">): string {
  if (p.probed_at === undefined) return "Not probed yet";
  const backend = backendLabels[p.backend];
  const build = p.capabilities.build?.split("-")[0];
  const parts = [
    build !== undefined && build !== "" ? `${backend} ${build}` : backend,
    `${String(p.slots)} slot${p.slots === 1 ? "" : "s"}`,
  ];
  if (p.capabilities.window !== undefined && p.capabilities.window > 0) {
    parts.push(`${formatCount(p.capabilities.window)} window`);
  }
  parts.push(`cache ${kvSummary(p.kv, p.kv_tokens)}`);
  return parts.join(" · ");
}

/** EndpointForm is what the endpoint settings hold while they are edited. */
export type EndpointForm = {
  backend: Backend;
  pool: string;
  slots: number;
  kv: KV;
  /** kvTokens is the shared cache's size; NaN while the field holds no number, 0 for not known. */
  kvTokens: number;
  api: API;
};

/** endpointForm starts the form from a provider as it stands. */
export function endpointForm(p: Provider): EndpointForm {
  return {
    backend: p.backend,
    pool: p.pool,
    slots: p.slots,
    kv: p.kv,
    kvTokens: p.kv_tokens ?? 0,
    api: p.api,
  };
}

/** EndpointProblem is what keeps the endpoint settings from saving, and where. */
export type EndpointProblem = { field: "slots" | "kvTokens" | "pool"; message: string };

/** maxPoolName mirrors the harness's bound on a pool name. */
export const maxPoolName = 64;

/** endpointProblem mirrors the harness's checks of the endpoint settings. */
export function endpointProblem(form: EndpointForm): EndpointProblem | null {
  if (!Number.isInteger(form.slots) || form.slots < 1 || form.slots > 1024) {
    return { field: "slots", message: "Enter a whole number of slots from 1 to 1024." };
  }
  if (
    form.kv === "shared" &&
    (!Number.isInteger(form.kvTokens) || form.kvTokens < 0 || form.kvTokens > 2_000_000_000)
  ) {
    return { field: "kvTokens", message: "Enter a whole number of tokens, or 0 if not known." };
  }
  if (form.pool.trim().length > maxPoolName) {
    return { field: "pool", message: `A pool name is at most ${String(maxPoolName)} characters.` };
  }
  return null;
}

/**
 * endpointUpdate is the PATCH that saves the form: only the fields that
 * differ from the provider, since a field sent is a field the user now owns
 * and a probe leaves alone. The pool is the one field that is compared to
 * what the provider's own name would give, so typing its default name back
 * does not pin it.
 */
export function endpointUpdate(p: Provider, form: EndpointForm): UpdateProvider {
  const out: UpdateProvider = {};
  if (form.backend !== p.backend) out.backend = form.backend;
  if (form.api !== p.api) out.api = form.api;
  if (form.slots !== p.slots) out.slots = form.slots;
  if (form.kv !== p.kv || (form.kv === "shared" && form.kvTokens !== (p.kv_tokens ?? 0))) {
    out.kv = form.kv;
    if (form.kv === "shared") out.kv_tokens = form.kvTokens;
  }
  const pool = form.pool.trim();
  if (pool !== p.pool) out.pool = pool === p.name ? "" : pool;
  return out;
}
