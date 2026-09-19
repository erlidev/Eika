/**
 * Named starting states for the UI. Each builds a fresh World; `path` is where
 * the scenario is most useful to open. Add a scenario when a screen needs data
 * no existing one has, and keep ids stable: specs and agents address them.
 */

import { emptyWorld, WorldBuilder } from "./world.ts";
import type { World } from "./world.ts";

/** Scenario is a starting state an agent or a spec can open by name. */
export type Scenario = {
  description: string;
  /** path is the URL the scenario is meant to be opened at. */
  path: string;
  build: () => World;
};

/** configured is a signed-in harness with one provider and two models. */
function configured(): WorldBuilder {
  const b = new WorldBuilder();
  const openai = b.provider({ name: "OpenAI" });
  b.model(openai, { name: "gpt-5", reasoning_effort: "medium" });
  b.model(openai, { name: "gpt-5-mini" });
  return b;
}

/**
 * workbench builds the richest state: two projects, workspaces in several
 * states, and a session whose transcript uses every kind of entry. Its ids
 * are fixed: the main session is `ses-backoff`, its workspace `ws-retries`.
 */
function workbench(): WorldBuilder {
  const b = configured();
  const api = b.project({ id: "proj-api", name: "payments-api" });
  const web = b.project({ name: "web-dashboard", kind: "local", host_path: "/srv/code/web" });
  b.workspace(web, { name: "dark-mode", state: "stopped" });
  const ws = b.workspace(api, { id: "ws-retries", name: "fix-retries" });
  b.workspace(api, { name: "upgrade-go", state: "creating" });
  const older = b.session(ws, { title: "Investigate flaky test" });
  b.conversation(older, [
    { role: "user", content: "Why does TestRetry fail sometimes?" },
    {
      role: "assistant",
      content: "It depends on wall-clock time; I'll switch it to a fake clock.",
    },
  ]);
  const main = b.session(ws, { id: "ses-backoff", title: "Add exponential backoff to retries" });
  b.conversation(main, [
    {
      role: "user",
      content:
        "The webhook client retries immediately on failure. Add exponential backoff with jitter, capped at 30s.",
    },
    { role: "assistant", content: "I'll start by finding the retry loop." },
    {
      role: "assistant",
      tool_calls: [
        { id: "c1", name: "bash", arguments: { command: "grep -rn 'retry' internal/webhook" } },
      ],
    },
    {
      role: "tool",
      tool_call_id: "c1",
      content:
        "internal/webhook/client.go:42:\tfor attempt := 0; attempt < maxRetries; attempt++ {\ninternal/webhook/client.go:51:\t\t// retry immediately",
    },
    {
      role: "assistant",
      tool_calls: [
        {
          id: "c2",
          name: "edit",
          arguments: {
            path: "internal/webhook/client.go",
            old_text: "\t\t// retry immediately\n\t\tcontinue",
            new_text: "\t\ttime.Sleep(backoff(attempt))\n\t\tcontinue",
          },
        },
      ],
    },
    { role: "tool", tool_call_id: "c2", content: "edited internal/webhook/client.go" },
    {
      role: "assistant",
      content:
        "Done. The client now waits `backoff(attempt)` between tries:\n\n| attempt | delay |\n|---|---|\n| 1 | ~1s |\n| 2 | ~2s |\n| 5 | 30s (cap) |\n\n```go\nfunc backoff(n int) time.Duration {\n\td := time.Second << n\n\treturn min(d, 30*time.Second)\n}\n```",
    },
  ]);
  b.world.diffs[ws.id] = {
    workspace_id: ws.id,
    base_commit: ws.base_commit,
    status: " M internal/webhook/client.go\n?? internal/webhook/backoff.go\n",
    diff: [
      "diff --git a/internal/webhook/client.go b/internal/webhook/client.go",
      "--- a/internal/webhook/client.go",
      "+++ b/internal/webhook/client.go",
      "@@ -48,7 +48,7 @@ func (c *Client) Send(ctx context.Context, e Event) error {",
      " \t\tif err == nil {",
      " \t\t\treturn nil",
      " \t\t}",
      "-\t\t// retry immediately",
      "+\t\ttime.Sleep(backoff(attempt))",
      " \t\tcontinue",
      " \t}",
      "",
    ].join("\n"),
  };
  return b;
}

export const scenarios = {
  "setup-new": {
    description: "A brand-new harness: no password yet, so the guided setup opens.",
    path: "/",
    build: () => ({
      ...emptyWorld(),
      passwordSet: false,
      signedIn: false,
      settings: { ...emptyWorld().settings, settings: {} },
    }),
  },
  "setup-signed-in": {
    description: "Password chosen but setup not finished: the setup wizard's later steps.",
    path: "/",
    build: () => {
      const w = emptyWorld();
      w.settings.settings = {};
      return w;
    },
  },
  "setup-docker-down": {
    description: "Setup wizard where Docker is unreachable and the sandbox image is missing.",
    path: "/",
    build: () => {
      const w = emptyWorld();
      w.settings.settings = {};
      w.system.docker = {
        reachable: false,
        error: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock",
      };
      w.system.sandbox_image.present = false;
      return w;
    },
  },
  "signed-out": {
    description: `Set-up harness, browser not signed in: the sign-in screen. Password "wrong" is refused.`,
    path: "/",
    build: () => ({ ...emptyWorld(), signedIn: false }),
  },
  empty: {
    description: "Signed in and set up, but no providers, models, or projects.",
    path: "/",
    build: () => emptyWorld(),
  },
  configured: {
    description: "One provider with two models, no projects yet.",
    path: "/",
    build: () => configured().world,
  },
  workbench: {
    description:
      "Projects, workspaces (running/stopped/creating), a diff, and session ses-backoff with a full transcript. Sending a message streams a mock reply.",
    path: "/sessions/ses-backoff",
    build: () => workbench().world,
  },
  "agent-tools": {
    description:
      "Like workbench, but the next message plays a reply that runs bash (streamed output), edits a file, and answers.",
    path: "/sessions/ses-backoff",
    build: () => {
      const b = workbench();
      b.world.replies.push([
        { say: "Let me run the tests first." },
        {
          tool: "bash",
          args: { command: "go test ./internal/webhook/..." },
          output: "=== RUN   TestBackoff\n--- PASS: TestBackoff (0.00s)\n",
          result: "ok  \tpayments/internal/webhook\t0.412s",
          details: { exit_code: 0 },
        },
        {
          tool: "bash",
          args: { command: "go vet ./..." },
          result: "internal/webhook/client.go:51: unreachable code",
          isError: true,
          details: { exit_code: 1 },
        },
        { say: "Tests pass. `go vet` flags unreachable code on line 51; I'll clean that up next." },
      ]);
      return b.world;
    },
  },
  "agent-question": {
    description:
      "Like workbench, but the next message makes the agent ask a multiple-choice question and wait.",
    path: "/sessions/ses-backoff",
    build: () => {
      const b = workbench();
      b.world.replies.push([
        { say: "Before I change the cap:" },
        {
          ask: "Which maximum delay should retries use?",
          options: ["10s", "30s", "60s"],
          allowFreeText: true,
        },
        { say: "Using that cap." },
      ]);
      return b.world;
    },
  },
  "agent-error": {
    description: "Like workbench, but the next message fails with a provider error.",
    path: "/sessions/ses-backoff",
    build: () => {
      const b = workbench();
      b.world.replies.push([
        { say: "Looking into it" },
        { fail: "provider: 429 Too Many Requests: rate limit exceeded", retryable: true },
      ]);
      return b.world;
    },
  },
  "agent-running": {
    description:
      "Like workbench, but the next message starts a run that never finishes (for the running state and abort).",
    path: "/sessions/ses-backoff",
    build: () => {
      const b = workbench();
      b.world.replies.push([{ say: "Working on it, this will take a while…" }, { hang: true }]);
      return b.world;
    },
  },
  "empty-session": {
    description: "A workspace with a session that has no messages yet.",
    path: "/sessions/ses-empty",
    build: () => {
      const b = configured();
      const p = b.project({ name: "payments-api" });
      const ws = b.workspace(p, { name: "scratch" });
      b.session(ws, { id: "ses-empty", title: "New session" });
      return b.world;
    },
  },
} satisfies Record<string, Scenario>;

/** ScenarioName is the name of a built-in scenario. */
export type ScenarioName = keyof typeof scenarios;

/** scenario looks a scenario up by name, listing the valid ones when it is unknown. */
export function scenario(name: string): Scenario {
  const found = (scenarios as Record<string, Scenario>)[name];
  if (!found) {
    throw new Error(`unknown scenario ${name}; known: ${Object.keys(scenarios).join(", ")}`);
  }
  return found;
}
