/** Agent definitions and user overrides. */
import type { Agent, AgentOverride } from "../../../src/api/types.ts";
import { builtinAgents } from "../agents.ts";
import { fail, ok } from "./context.ts";
import type { RouteContext } from "./context.ts";

export function agentRoutes({ w, on }: RouteContext): void {
  const view = (name: string | undefined): Agent | undefined => {
    const builtin = builtinAgents.find((d) => d.name === name);
    if (!builtin) return undefined;
    const overrides = w.agentOverrides[builtin.name] ?? { class: null, prompt: null };
    return {
      ...builtin,
      class: overrides.class ?? builtin.class,
      prompt: overrides.prompt ?? builtin.prompt,
      builtin,
      overrides,
    };
  };
  const missing = () => fail(404, "not_found", "agent does not exist");
  on("GET", "/api/agents", () => ok({ agents: builtinAgents.map((d) => view(d.name)) }));
  on("GET", "/api/agents/{name}", ({ params }) => {
    const agent = view(params[0]);
    return agent ? ok(agent) : missing();
  });
  on("PUT", "/api/agents/{name}", ({ params, body }) => {
    const agent = view(params[0]);
    if (!agent) return missing();
    const cls = body.class ?? null;
    if (cls !== null && cls !== "main" && cls !== "fast" && cls !== "deep") {
      return fail(400, "invalid_request", "class must be main, fast or deep");
    }
    const prompt = body.prompt ?? null;
    if (
      prompt !== null &&
      (typeof prompt !== "string" || new TextEncoder().encode(prompt).length > 65536)
    ) {
      return fail(400, "invalid_request", "prompt must be at most 65536 bytes");
    }
    const overrides: AgentOverride = { class: cls, prompt };
    if (cls === null && prompt === null) Reflect.deleteProperty(w.agentOverrides, agent.name);
    else w.agentOverrides[agent.name] = overrides;
    return ok(view(agent.name));
  });
  on("DELETE", "/api/agents/{name}", ({ params }) => {
    const agent = view(params[0]);
    if (!agent) return missing();
    Reflect.deleteProperty(w.agentOverrides, agent.name);
    return { status: 204 };
  });
}
