/** Providers and their models, as internal/server/providers.go serves them. */

import type { Model, Provider, ThinkingSwitch } from "../../../src/api/types.ts";
import { fail, ok, str, strings } from "./context.ts";
import type { Reply, RouteContext } from "./context.ts";

/** thinkingSwitch narrows a JSON body field to a switch, standard when absent. */
function thinkingSwitch(value: unknown): ThinkingSwitch {
  return value === "chat_template_kwargs" || value === "thinking" ? value : "reasoning_effort";
}

export function providerRoutes(ctx: RouteContext): void {
  const { w, on, find, now } = ctx;
  on("GET", "/api/providers", () => ok({ providers: w.providers, kinds: w.providerKinds }));
  on("POST", "/api/providers/probe", () => ok({ models: w.probeModels }));
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
    row.updated_at = now();
    return ok(row);
  });
  on("DELETE", "/api/providers/{id}", ({ params }) => {
    w.providers = w.providers.filter((p) => p.id !== params[0]);
    w.models = w.models.filter((m) => m.provider_id !== params[0]);
    return { status: 204 };
  });
  on("GET", "/api/models", () =>
    ok({ models: w.models, ...(w.defaultModel ? { default: w.defaultModel } : {}) }),
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
      max_output: Number(body.max_output) || 0,
      reasoning_effort: str(body.reasoning_effort),
      reasoning_efforts: strings(body.reasoning_efforts),
      thinking_switch: thinkingSwitch(body.thinking_switch),
      preserve_thinking: body.preserve_thinking === true,
      created_at: now(),
      updated_at: now(),
    };
    w.models.push(row);
    w.defaultModel ??= row.name;
    return ok(row, 201);
  });
  on("PATCH", "/api/models/{id}", ({ params, body }) => {
    const row = find(w.models, params[0], "model");
    if ("status" in row) return row;
    Object.assign(row, body, { updated_at: now() });
    return ok(row);
  });
  on("DELETE", "/api/models/{id}", ({ params }) => {
    w.models = w.models.filter((m) => m.id !== params[0]);
    return { status: 204 };
  });
}
