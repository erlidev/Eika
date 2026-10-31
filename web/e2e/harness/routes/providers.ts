/** Providers and their models, as internal/server/providers.go serves them. */

import type {
  Backend,
  EndpointField,
  KV,
  Model,
  Provider,
  ThinkingSwitch,
} from "../../../src/api/types.ts";
import { windowOf } from "../world.ts";
import { fail, ok, str, strings } from "./context.ts";
import type { Reply, RouteContext } from "./context.ts";

/** thinkingSwitch narrows a JSON body field to a switch, standard when absent. */
function thinkingSwitch(value: unknown): ThinkingSwitch {
  return value === "chat_template_kwargs" || value === "thinking" ? value : "reasoning_effort";
}

const endpointFields: EndpointField[] = ["backend", "pool", "slots", "kv", "api"];

/** overrideProblem mirrors validateOverride in internal/server/providers.go. */
function overrideProblem(body: Record<string, unknown>): Reply | undefined {
  const bad = (message: string) => fail(400, "invalid_request", message);
  if (typeof body.slots === "number" && (body.slots < 1 || body.slots > 1024)) {
    return bad("slots must be between 1 and 1024");
  }
  if (typeof body.kv === "string" && !["shared", "partitioned", "unknown"].includes(body.kv)) {
    return bad("kv must be one of shared, partitioned, unknown");
  }
  if (typeof body.kv_tokens === "number" && body.kv_tokens > 0 && body.kv !== "shared") {
    return bad("kv_tokens is the size of a shared cache; set kv to shared with it");
  }
  if (typeof body.api === "string" && !["responses", "chat"].includes(body.api)) {
    return bad("api must be one of responses, chat");
  }
  return undefined;
}

/** applyOverride records what the user set and hands back what they reset. */
function applyOverride(row: Provider, body: Record<string, unknown>): void {
  const mark = (field: EndpointField) => {
    if (!row.overridden.includes(field)) row.overridden.push(field);
  };
  if (typeof body.backend === "string") {
    row.backend = body.backend as Backend;
    mark("backend");
  }
  if (body.api === "responses" || body.api === "chat") {
    row.api = body.api;
    mark("api");
  }
  if (typeof body.pool === "string") {
    // The harness stores "" for the default, which reads as the provider's name.
    const pool = body.pool.trim();
    row.pool = pool === "" ? row.name : pool;
    row.overridden = row.overridden.filter((f) => f !== "pool");
    if (pool !== "") mark("pool");
  }
  if (typeof body.slots === "number") {
    row.slots = body.slots;
    mark("slots");
  }
  if (typeof body.kv === "string" || typeof body.kv_tokens === "number") {
    if (typeof body.kv === "string") row.kv = body.kv as KV;
    if (typeof body.kv_tokens === "number") row.kv_tokens = body.kv_tokens;
    if (row.kv !== "shared" || !row.kv_tokens) delete row.kv_tokens;
    mark("kv");
  }
  for (const field of strings(body.reset)) {
    if (endpointFields.includes(field as EndpointField)) {
      row.overridden = row.overridden.filter((f) => f !== field);
      if (field === "pool") row.pool = row.name;
    }
  }
}

export function providerRoutes(ctx: RouteContext): void {
  const { w, on, find, now } = ctx;
  on("GET", "/api/providers", () => ok({ providers: w.providers, kinds: w.providerKinds }));
  // As internal/server/providers_probe.go records a probe: the user's own
  // values stay, and only the probe's own fields are replaced.
  on("POST", "/api/providers/probe", ({ body }) => {
    const row = w.providers.find((p) => p.id === body.provider_id);
    const finds = w.probeFinds;
    const sameEndpoint =
      typeof body.base_url !== "string" || body.base_url.trim() === row?.base_url;
    // A key typed into a form is being tried; nothing is recorded for it.
    if (row && sameEndpoint && typeof body.api_key !== "string") {
      if (!row.overridden.includes("backend")) row.backend = finds.backend;
      if (!row.overridden.includes("slots")) row.slots = finds.slots;
      if (!row.overridden.includes("api")) row.api = finds.api;
      if (!row.overridden.includes("kv")) {
        row.kv = finds.kv;
        if (finds.kv_tokens !== undefined) row.kv_tokens = finds.kv_tokens;
        else delete row.kv_tokens;
      }
      row.capabilities = finds.capabilities;
      row.probed_at = now();
      delete row.probe_error;
      return ok({ models: w.probeModels, probe: finds, provider: row });
    }
    return ok({ models: w.probeModels, probe: finds });
  });
  // As internal/server/providers.go validates a provider.
  const providerProblem = (name: string, baseUrl: string, id?: string): Reply | undefined => {
    if (name === "") return fail(400, "invalid_request", "name must be 1 to 64 characters");
    if (baseUrl === "") {
      return fail(
        400,
        "invalid_request",
        "base_url is required, such as https://api.openai.com/v1",
      );
    }
    let url: URL;
    try {
      url = new URL(baseUrl);
    } catch {
      url = new URL("invalid:");
    }
    if (url.protocol !== "http:" && url.protocol !== "https:") {
      return fail(
        400,
        "invalid_request",
        "base_url must be an http or https URL, such as https://api.openai.com/v1",
      );
    }
    if (url.username !== "" || url.password !== "") {
      return fail(
        400,
        "invalid_request",
        "base_url must not contain credentials; put the key in api_key",
      );
    }
    if (url.search !== "" || url.hash !== "" || baseUrl.includes("?") || baseUrl.includes("#")) {
      return fail(400, "invalid_request", "base_url must not contain a query string or fragment");
    }
    if (w.providers.some((p) => p.name === name && p.id !== id)) {
      return fail(409, "conflict", `a provider named "${name}" already exists`);
    }
    return undefined;
  };
  const keyHint = (key: string) => (key.length >= 16 ? { api_key_hint: key.slice(-4) } : {});

  on("POST", "/api/providers", ({ body }) => {
    const problem = providerProblem(str(body.name).trim(), str(body.base_url).trim());
    if (problem) return problem;
    const key = str(body.api_key);
    const row: Provider = {
      id: ctx.nextId("prov"),
      name: str(body.name),
      kind: str(body.kind) || "openai",
      base_url: str(body.base_url),
      api_key_set: key !== "",
      ...keyHint(key),
      // Nobody has probed it yet.
      backend: "other",
      pool: str(body.name),
      slots: 1,
      kv: "unknown",
      api: "chat",
      capabilities: { counting: "none", cache_metrics: "unknown", slot_hints: false },
      overridden: [],
      created_at: now(),
      updated_at: now(),
    };
    w.providers.push(row);
    return ok(row, 201);
  });
  on("PATCH", "/api/providers/{id}", ({ params, body }) => {
    const row = find(w.providers, params[0], "provider");
    if ("status" in row) return row;
    const problem = providerProblem(
      typeof body.name === "string" ? body.name.trim() : row.name,
      typeof body.base_url === "string" ? body.base_url.trim() : row.base_url,
      row.id,
    );
    if (problem) return problem;
    const override = overrideProblem(body);
    if (override) return override;
    if (typeof body.name === "string") row.name = body.name.trim();
    if (typeof body.base_url === "string") {
      // As the harness does: a new URL without a new key clears the key.
      if (body.base_url.trim() !== row.base_url && typeof body.api_key !== "string") {
        row.api_key_set = false;
        delete row.api_key_hint;
      }
      row.base_url = body.base_url.trim();
    }
    if (typeof body.api_key === "string") {
      row.api_key_set = body.api_key !== "";
      delete row.api_key_hint;
      Object.assign(row, keyHint(body.api_key));
    }
    applyOverride(row, body);
    row.updated_at = now();
    return ok(row);
  });
  on("DELETE", "/api/providers/{id}", ({ params }) => {
    w.providers = w.providers.filter((p) => p.id !== params[0]);
    w.models = w.models.filter((m) => m.provider_id !== params[0]);
    return { status: 204 };
  });
  /** served is a model with the windows its provider's probe gives it. */
  const served = (m: Model): Model => {
    const { probed, effective } = windowOf(w, m);
    return {
      ...m,
      ...(probed === undefined ? {} : { probed_window: probed }),
      effective_window: effective,
    };
  };
  on("GET", "/api/models", () =>
    ok({ models: w.models.map(served), ...(w.defaultModel ? { default: w.defaultModel } : {}) }),
  );
  on("POST", "/api/models/test", ({ body }) =>
    ok({ reply: `Hello from ${str(body.model)}.`, stop_reason: "stop", latency_ms: 412 }),
  );
  on("POST", "/api/models", ({ body }) => {
    const name = str(body.name) || str(body.model);
    if (w.models.some((m) => m.name === name)) {
      return fail(409, "conflict", `a model named ${name} exists`);
    }
    const row: Model = {
      id: ctx.nextId("mod"),
      provider_id: str(body.provider_id),
      name,
      model: str(body.model),
      context_window: Number(body.context_window) || 0,
      effective_window: Number(body.context_window) || 0,
      max_output: Number(body.max_output) || 0,
      reasoning_effort: str(body.reasoning_effort),
      reasoning_efforts: strings(body.reasoning_efforts),
      thinking_switch: thinkingSwitch(body.thinking_switch),
      preserve_thinking: body.preserve_thinking === true,
      image_input: body.image_input === true,
      created_at: now(),
      updated_at: now(),
    };
    w.models.push(row);
    w.defaultModel ??= row.name;
    return ok(served(row), 201);
  });
  on("PATCH", "/api/models/{id}", ({ params, body }) => {
    const row = find(w.models, params[0], "model");
    if ("status" in row) return row;
    Object.assign(row, body, { updated_at: now() });
    return ok(served(row));
  });
  on("DELETE", "/api/models/{id}", ({ params }) => {
    w.models = w.models.filter((m) => m.id !== params[0]);
    return { status: 204 };
  });
}
