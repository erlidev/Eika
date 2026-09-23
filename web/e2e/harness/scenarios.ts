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
  b.model(openai, {
    name: "gpt-5",
    reasoning_effort: "medium",
    reasoning_efforts: ["low", "medium", "high"],
  });
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
    {
      role: "assistant",
      reasoning:
        "The client retries in a loop with no delay. I need to find that loop before I can put a delay in it, so grep for retry under internal/webhook first.",
      content: "I'll start by finding the retry loop.",
    },
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
  // A fork of the older session, and a child agent working in a workspace of
  // its own: both hang under the session they came from in the sidebar, and
  // the child's workspace is reached through it rather than listed beside
  // its parent's.
  const forked = b.session(ws, {
    title: "Investigate flaky test, pinned seed",
    kind: "fork",
    parent_session_id: older.id,
  });
  b.conversation(forked, [
    { role: "user", content: "Why does TestRetry fail sometimes?" },
    { role: "assistant", content: "Trying a different tack: pin the seed instead." },
  ]);
  const childWS = b.workspace(api, {
    name: "add-backoff-tests",
    branch: "eika/fix-retries-add-backoff-tests-a1b2c3",
    parent_workspace_id: ws.id,
  });
  const child = b.session(childWS, {
    title: "add-backoff-tests",
    kind: "agent",
    parent_session_id: main.id,
  });
  b.conversation(child, [
    { role: "user", content: "Write table tests for backoff(n), including the 30s cap." },
    { role: "assistant", content: "Added TestBackoff with six cases; all pass." },
  ]);

  b.world.files[ws.id] = {
    ".git/HEAD": "ref: refs/heads/eika/fix-retries\n",
    ".gitignore": "/bin\n*.out\n",
    "README.md": "# payments-api\n\nThe payments service and its webhook client.\n",
    "go.mod": "module example.com/payments-api\n\ngo 1.23\n",
    "assets/logo.png": "\u0089PNG\r\n\u001a\n\u0000\u0000\u0000\rIHDR",
    "internal/webhook/backoff.go": [
      "package webhook",
      "",
      'import "time"',
      "",
      "// backoff is how long to wait before retry n: doubling from a second, capped at 30s.",
      "func backoff(n int) time.Duration {",
      "\td := time.Second << n",
      "\treturn min(d, 30*time.Second)",
      "}",
      "",
    ].join("\n"),
    "internal/webhook/client.go": [
      "package webhook",
      "",
      "import (",
      '\t"context"',
      '\t"time"',
      ")",
      "",
      "// Client delivers events to a subscriber's endpoint.",
      "type Client struct {",
      "\tmaxRetries int",
      "}",
      "",
      "// Send delivers one event, retrying with backoff.",
      "func (c *Client) Send(ctx context.Context, e Event) error {",
      "\tvar err error",
      "\tfor attempt := 0; attempt < c.maxRetries; attempt++ {",
      "\t\terr = c.post(ctx, e)",
      "\t\tif err == nil {",
      "\t\t\treturn nil",
      "\t\t}",
      "\t\ttime.Sleep(backoff(attempt))",
      "\t\tcontinue",
      "\t}",
      "\treturn err",
      "}",
      "",
    ].join("\n"),
    "internal/webhook/client_test.go":
      'package webhook\n\nimport "testing"\n\nfunc TestSend(t *testing.T) {}\n',
  };
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
    description:
      "Setup wizard resumed at its Sandbox step (a provider and a model exist), with Docker unreachable and the sandbox image missing.",
    path: "/",
    build: () => {
      const w = configured().world;
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
  "agent-reasoning": {
    description:
      "Like workbench, but the next message streams reasoning before the answer. For the collapsed thinking block and the context meter.",
    path: "/sessions/ses-backoff",
    build: () => {
      const b = workbench();
      b.world.replies.push([
        {
          think:
            "The cap is 30s and the base is a second, so attempt five already reaches it. Doubling past that buys nothing, and the jitter has to stay inside the cap or a retry can overshoot it. Full jitter over the capped delay is the usual answer.",
        },
        {
          say: "Full jitter over the capped delay: `rand(0, min(2^n, 30s))`. That keeps every retry inside the cap and spreads the herd.",
        },
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
  "agent-cut-off": {
    description:
      "Like workbench, but the next message's answer is cut off at the model's output limit: what streamed is kept and the transcript says why it stopped.",
    path: "/sessions/ses-backoff",
    build: () => {
      const b = workbench();
      b.world.replies.push([
        { think: "The file is about 35KB. I will write it in one go." },
        { say: "Writing the file now. It starts like this:" },
        { cutOff: "length" },
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
  providers: {
    description:
      "Three providers: OpenAI with a stored key and two models, a local vLLM that needs no key, and OpenRouter whose key is missing. For the Models tab, ProviderForm, and ModelPicker.",
    path: "/",
    build: () => {
      const b = configured();
      b.provider({
        name: "Local vLLM",
        base_url: "http://localhost:8000/v1",
        api_key_set: false,
        api_key_hint: undefined,
      });
      const router = b.provider({
        name: "OpenRouter",
        base_url: "https://openrouter.ai/api/v1",
        api_key_set: false,
        api_key_hint: undefined,
      });
      b.model(router, { name: "claude-sonnet", model: "anthropic/claude-sonnet-4.5" });
      return b.world;
    },
  },
  "agent-web": {
    description:
      "Like workbench with an abandoned branch in the session tree; the next message plays a reply that uses web_search, web_fetch, read, grep, find, ls, write, and a tool with no renderer of its own. Expand a card by its summary to see its body.",
    path: "/sessions/ses-backoff",
    build: () => {
      const b = workbench();
      // An abandoned first answer, so the session tree has a branch.
      const entries = b.world.entries["ses-backoff"] ?? [];
      const first = entries[0];
      if (first) {
        entries.push({
          id: "ent-abandoned",
          seq: entries.length + 1,
          kind: "assistant",
          parent_id: first.id,
          created_at: first.created_at,
          message: { role: "assistant", content: "A constant one-second delay would do." },
        });
      }
      b.world.replies.push([
        { say: "Let me check how other clients cap their backoff." },
        {
          tool: "web_search",
          args: { query: "exponential backoff jitter cap", count: 3 },
          result: "1. Exponential Backoff And Jitter\n2. Retry strategies\n3. backoff package",
          details: {
            source: "web",
            query: "exponential backoff jitter cap",
            count: 3,
            providers: ["searxng"],
            attempts: [{ provider: "exa", error: "no API key" }],
            results: [
              {
                title: "Exponential Backoff And Jitter",
                url: "https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/",
                description:
                  "Adding jitter to exponential backoff spreads retries out, so clients that failed together do not retry together and fail again.",
              },
              {
                title:
                  "Retry strategies for distributed systems, with a very long title that has to wrap or truncate somewhere",
                url: "https://example.com/a/very/long/path/that/keeps/going/and/going/until/it/cannot/fit/on/one/line/retry-strategies.html",
              },
              {
                title: "backoff package",
                url: "https://pkg.go.dev/github.com/cenkalti/backoff/v4",
              },
            ],
            ms: 412,
          },
        },
        {
          tool: "web_fetch",
          args: {
            url: "https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/",
            section: "Full Jitter",
          },
          result:
            "## Full Jitter\n\nsleep = random_between(0, min(cap, base * 2 ** attempt))\n\nFull jitter does the least work and spreads calls best.",
          details: {
            url: "https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/",
            final_url: "https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter",
            format: "markdown",
            section: "Full Jitter",
            section_matched: true,
            container: "article",
            mode: "full",
            ms: 845,
          },
        },
        {
          tool: "read",
          args: { path: "internal/webhook/client.go", offset: 40, limit: 20 },
          result:
            "40\tfunc (c *Client) Send(ctx context.Context, e Event) error {\n41\t\tvar err error",
        },
        {
          tool: "grep",
          args: { pattern: "maxRetries", path: "internal", ignore_case: true },
          result: "internal/webhook/client.go:12:const maxRetries = 5",
        },
        {
          tool: "find",
          args: { pattern: "*_test.go", path: "internal/webhook" },
          result: "internal/webhook/client_test.go\ninternal/webhook/backoff_test.go",
        },
        {
          tool: "ls",
          args: { path: "internal/webhook" },
          result: "backoff.go\nclient.go\nclient_test.go",
        },
        {
          tool: "write",
          args: {
            path: "internal/webhook/backoff.go",
            content: "package webhook\n\n// backoff is full jitter, capped at 30s.\n",
          },
          result: "wrote internal/webhook/backoff.go",
        },
        {
          tool: "spawn_agent",
          args: { task: "Review the backoff change", model: "gpt-5-mini" },
          result: '{"status":"done"}',
        },
        { say: "Full jitter it is: the cap stays at 30s." },
      ]);
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
