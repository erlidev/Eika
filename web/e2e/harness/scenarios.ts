/**
 * Named starting states for the UI. Each builds a fresh World; `path` is where
 * the scenario is most useful to open. Add a scenario when a screen needs data
 * no existing one has, and keep ids stable: specs and agents address them.
 */

import { noSettings, syncSession } from "./profiles.ts";
import { emptyWorld, minutesAgo, sessionTools, WorldBuilder } from "./world.ts";
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
          name: "bash",
          arguments: {
            command:
              "sed -i 's|// retry immediately|time.Sleep(backoff(attempt))|' internal/webhook/client.go && sed -n 50,52p internal/webhook/client.go",
          },
        },
      ],
    },
    {
      role: "tool",
      tool_call_id: "c2",
      content: "\t\ttime.Sleep(backoff(attempt))\n\t\tcontinue\n\t}",
    },
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

/**
 * profiles adds two profiles to a workbench, puts ses-backoff on one with an
 * override of its own, and records two of its model calls: one when only
 * its first message had been sent, and one at its head.
 */
function profiles(): WorldBuilder {
  const b = workbench();
  const w = b.world;
  const mini = w.models.find((m) => m.name === "gpt-5-mini");
  // A model whose own values differ, so an editor that picks it shows them.
  if (mini) mini.max_output = 64000;
  const reviewer = b.profile({
    name: "Reviewer",
    description: "Reads and reviews; changes nothing",
    instructions: "Review the change and report problems. Do not edit files.",
    sampling: { temperature: 0.2 },
    tools: ["bash", "web_fetch"],
  });
  b.profile({
    name: "Fast",
    ...(mini ? { model_id: mini.id } : {}),
    sampling: { reasoning_effort: "low", max_output: 4096 },
  });
  const ws = w.workspaces.find((x) => x.id === "ws-retries");
  const main = w.sessions.find((x) => x.id === "ses-backoff");
  if (!ws || !main) throw new Error("the workbench has no ses-backoff");
  (w.files[ws.id] ??= {})["AGENTS.md"] =
    "# payments-api\n\nRun `go test ./...` before you report. Keep the webhook client free of global state.\n";
  main.profile_id = reviewer.id;
  w.overrides[main.id] = { ...noSettings(), sampling: { temperature: 0.7 } };
  syncSession(w, main);
  const head = main.head_entry_id;
  const first = (w.entries[main.id] ?? [])[0];
  if (first) main.head_entry_id = first.id;
  b.request(main, 40, 212);
  main.head_entry_id = head;
  b.request(main, 12, 318);
  return b;
}

/**
 * chats adds two chats and a fork of one to a workbench: sessions with no
 * workspace, listed apart from the projects. The main one is `chat-jitter`.
 */
function chats(): WorldBuilder {
  const b = workbench();
  b.chat({
    id: "chat-postgres",
    title: "Postgres advisory locks",
    created_at: "2026-03-13T09:00:00.000Z",
  });
  const main = b.chat({ id: "chat-jitter", title: "What is full jitter?" });
  b.conversation(main, [
    { role: "user", content: "What is full jitter, and why do retry clients use it?" },
    {
      role: "assistant",
      tool_calls: [
        {
          id: "w1",
          name: "web_search",
          arguments: { query: "full jitter exponential backoff", count: 3 },
        },
      ],
    },
    {
      role: "tool",
      tool_call_id: "w1",
      content:
        "1. Exponential Backoff And Jitter\n   https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/\n   Adding jitter spreads retries out.",
    },
    {
      role: "assistant",
      content:
        "Full jitter picks each delay at random between zero and the capped exponential backoff:\n\n```text\nsleep = random_between(0, min(cap, base * 2 ** attempt))\n```\n\nClients that failed together then retry at different times instead of failing together again ([AWS Architecture Blog](https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/)).",
    },
  ]);
  b.chat({
    id: "chat-jitter-fork",
    title: "What is full jitter? (fork)",
    kind: "fork",
    parent_session_id: main.id,
    tools: ["web_search"],
    created_at: "2026-03-14T14:30:00.000Z",
  });
  return b;
}

/** chartPNG is a 96x48 bar chart, as an MCP tool might send one. */
const chartPNG =
  "iVBORw0KGgoAAAANSUhEUgAAAGAAAAAwCAIAAABhdOiYAAAAkklEQVR42u3awQmAIACGURdoy0ZoiKBrq0bQqU4REYgYaPnguwiC/O9sWNbtrB+m61FHARAgQM0CdfP4GCBAgAABAlQcKLofECBAgH4OFH0CECBAXwXK3w8IECBAVQPlCwICBOirQDXsBwQIEKCqgfIvAAIEqAxQI/sBAQIECBAgQIAAAXoHSLeCv/QJ/4MEKLkdyOnW8ZEYQV0AAAAASUVORK5CYII=";

/**
 * mcpServers adds the MCP servers the settings and the transcript are shown
 * with: `github`, a remote server that is connected and signed in, with
 * tools, resources, and prompts; `linear`, a remote one that needs a sign-in;
 * `filesystem`, a stdio one that ran in fix-retries; `sentry`, whose last
 * connection failed; and `docs`, which is off.
 */
function mcpServers(b: WorldBuilder): void {
  const tool = (
    server: string,
    name: string,
    description: string,
    annotations?: Partial<{
      read_only: boolean;
      destructive: boolean;
      idempotent: boolean;
      open_world: boolean;
    }>,
    enabled = true,
  ) => ({
    name,
    exposed_name: `mcp__${server}__${name}`,
    description,
    input_schema: {
      type: "object",
      properties: { query: { type: "string", description: "What to look for." } },
      required: ["query"],
    },
    ...(annotations === undefined
      ? {}
      : {
          annotations: {
            read_only: annotations.read_only ?? null,
            destructive: annotations.destructive ?? null,
            idempotent: annotations.idempotent ?? null,
            open_world: annotations.open_world ?? null,
          },
        }),
    enabled,
  });
  const capabilities = {
    tools: true,
    tools_list_changed: true,
    resources: true,
    resources_subscribe: false,
    resources_list_changed: false,
    prompts: true,
    prompts_list_changed: false,
    logging: false,
    completions: false,
    experimental: [],
    extensions: [],
  };
  b.mcpServer(
    {
      id: "mcp-github",
      name: "github",
      kind: "http",
      url: "https://api.githubcopilot.com/mcp/",
      state: "connected",
      disabled_tools: ["merge_pull_request"],
    },
    {
      connection: {
        era: "modern",
        transport: "streamable_http",
        protocol_version: "2026-07-28",
        supported_versions: ["2026-07-28", "2025-11-25"],
        server_info: {
          name: "github-mcp-server",
          title: "GitHub",
          version: "1.4.0",
          website_url: "https://github.com/github/github-mcp-server",
        },
        capabilities,
        instructions: "Prefer search_issues over listing every issue of a repository.",
      },
      tools: [
        tool("github", "search_issues", "Search issues and pull requests across GitHub.", {
          read_only: true,
          open_world: true,
        }),
        tool("github", "create_issue", "Open an issue in a repository.", {
          destructive: false,
          open_world: true,
        }),
        tool(
          "github",
          "merge_pull_request",
          "Merge a pull request into its base branch.",
          { destructive: true, idempotent: false },
          false,
        ),
      ],
      resources: [
        {
          uri: "repo://example/payments-api/README.md",
          name: "README.md",
          title: "payments-api README",
          description: "The repository's front page.",
          mime_type: "text/markdown",
          size: 2048,
        },
      ],
      resource_templates: [
        {
          uri_template: "repo://{owner}/{repo}/contents/{path}",
          name: "file",
          title: "Repository file",
          description: "Any file of a repository.",
        },
      ],
      prompts: [
        {
          name: "summarize_pr",
          title: "Summarize a pull request",
          description: "Summarize a pull request's change and its review.",
          arguments: [
            { name: "repo", description: "owner/name", required: true },
            { name: "number", description: "the pull request number", required: true },
          ],
        },
      ],
      fetched_at: minutesAgo(12),
      logs: [
        {
          time: minutesAgo(12),
          source: "eika",
          level: "info",
          text: "connected over Streamable HTTP, protocol 2026-07-28",
        },
        {
          time: minutesAgo(12),
          source: "eika",
          level: "info",
          text: "listed 3 tools, 1 resource, 1 prompt",
        },
      ],
      auth: {
        challenged: true,
        challenge: {
          resource_metadata:
            "https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/",
          scope: "repo",
        },
        authorized: true,
        has_refresh_token: true,
        issuer: "https://github.com/login/oauth",
        resource: "https://api.githubcopilot.com/mcp/",
        scope: "repo read:org",
        expires_at: "2026-03-14T23:00:00.000Z",
        client_id: "Iv1.eika7f3a",
        registration: "dynamic",
        updated_at: minutesAgo(12),
      },
    },
  );
  b.mcpServer(
    {
      id: "mcp-linear",
      name: "linear",
      kind: "http",
      url: "https://mcp.linear.app/mcp",
      state: "unauthorized",
      error: "the server answered 401: sign in to use it",
    },
    {
      auth: {
        challenged: true,
        challenge: {
          resource_metadata: "https://mcp.linear.app/.well-known/oauth-protected-resource",
          error: "invalid_token",
        },
        authorized: false,
        has_refresh_token: false,
        issuer: "https://mcp.linear.app",
      },
      logs: [
        {
          time: minutesAgo(3),
          source: "eika",
          level: "warning",
          text: "401 Unauthorized: the server needs authorization",
        },
      ],
    },
  );
  b.mcpServer(
    {
      id: "mcp-filesystem",
      name: "filesystem",
      kind: "stdio",
      command: "npx",
      args: ["-y", "@modelcontextprotocol/server-filesystem", "."],
      env_names: ["LOG_LEVEL"],
      state: "idle",
    },
    {
      connection: {
        era: "legacy",
        transport: "stdio",
        protocol_version: "2025-06-18",
        supported_versions: [],
        server_info: { name: "secure-filesystem-server", version: "0.6.2" },
        capabilities: { ...capabilities, resources: false, prompts: false },
      },
      tools: [
        tool("filesystem", "read_text_file", "Read a text file of the allowed directories.", {
          read_only: true,
        }),
        tool("filesystem", "write_file", "Create or overwrite a file.", { destructive: true }),
      ],
      fetched_at: minutesAgo(40),
      logs: [
        {
          time: minutesAgo(41),
          source: "stderr",
          level: "info",
          text: "Secure MCP Filesystem Server running on stdio",
        },
        {
          time: minutesAgo(41),
          source: "stderr",
          level: "info",
          text: "Allowed directories: [ '/workspace' ]",
        },
      ],
    },
  );
  b.mcpServer(
    {
      id: "mcp-sentry",
      name: "sentry",
      kind: "http",
      url: "https://sentry.internal/mcp",
      state: "error",
      error: "connect: dial tcp: lookup sentry.internal: no such host",
      header_names: ["Authorization"],
    },
    {
      logs: [
        {
          time: minutesAgo(2),
          source: "eika",
          level: "error",
          text: "connect: dial tcp: lookup sentry.internal: no such host",
        },
      ],
    },
  );
  b.mcpServer({
    id: "mcp-docs",
    name: "docs",
    kind: "http",
    url: "https://docs.example.com/mcp",
    enabled: false,
    state: "disabled",
  });
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
      "Like workbench with an abandoned branch in the session tree; the next message plays a reply that uses web_search, web_fetch, bash to read, search, and write files, and a tool with no renderer of its own. Expand a card by its summary to see its body.",
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
          tool: "bash",
          args: {
            command: "rg -n -i maxretries internal && sed -n 40,41p internal/webhook/client.go",
          },
          result:
            "internal/webhook/client.go:12:const maxRetries = 5\nfunc (c *Client) Send(ctx context.Context, e Event) error {\n\tvar err error",
          details: { exit_code: 0 },
        },
        {
          tool: "bash",
          args: {
            command:
              "cat > internal/webhook/backoff.go <<'EOF'\npackage webhook\n\n// backoff is full jitter, capped at 30s.\nEOF\nls internal/webhook",
          },
          result: "backoff.go\nclient.go\nclient_test.go",
          details: { exit_code: 0 },
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
  chat: {
    description:
      "Like workbench with two chats and a fork in the sidebar's Chats section; chat-jitter is open, and the next message plays a reply that searches the web and answers.",
    path: "/sessions/chat-jitter",
    build: () => {
      const b = chats();
      b.world.replies.push([
        {
          tool: "web_search",
          args: { query: "decorrelated jitter", count: 3 },
          result: "1. Exponential Backoff And Jitter",
          details: {
            source: "web",
            query: "decorrelated jitter",
            count: 3,
            providers: ["searxng"],
            results: [
              {
                title: "Exponential Backoff And Jitter",
                url: "https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/",
                description: "Decorrelated jitter grows each delay from the one before it.",
              },
            ],
            ms: 380,
          },
        },
        {
          say: "Decorrelated jitter grows each delay from the last one rather than from the attempt number.",
        },
      ]);
      return b.world;
    },
  },
  mcp: {
    description:
      "Like workbench with five MCP servers: github (connected, signed in, tools, resources, a prompt), linear (needs sign-in), filesystem (stdio), sentry (failed), and docs (off). Open Settings, MCP.",
    path: "/sessions/ses-backoff",
    build: () => {
      const b = workbench();
      mcpServers(b);
      return b.world;
    },
  },
  "chat-mcp": {
    description:
      "Like chat, with the mcp scenario's servers: the chat's Tools panel lists the remote servers' tools under each server.",
    path: "/sessions/chat-jitter",
    build: () => {
      const b = chats();
      mcpServers(b);
      // A chat that never chose its tools offers every one it can, the
      // servers' included, as the harness reports it.
      for (const chat of b.world.sessions.filter((x) => x.id === "chat-jitter")) {
        chat.tools = sessionTools(b.world, true);
      }
      return b.world;
    },
  },
  "agent-mcp": {
    description:
      "Like mcp, but the next message calls github's create_issue, which asks for input in a form first, then search_issues, which answers with text and an image.",
    path: "/sessions/ses-backoff",
    build: () => {
      const b = workbench();
      mcpServers(b);
      b.world.replies.push([
        { say: "I'll file the flaky test as an issue." },
        {
          mcp: { server: "github", tool: "create_issue" },
          args: { repo: "example/payments-api", title: "TestRetry is flaky" },
          content: [{ type: "text", text: "Opened example/payments-api#42." }],
          structured: { number: 42, url: "https://github.com/example/payments-api/issues/42" },
          elicit: {
            mode: "form",
            message: "Which labels and assignee should the issue get?",
            requested_schema: {
              type: "object",
              properties: {
                labels: {
                  type: "array",
                  title: "Labels",
                  items: { type: "string", enum: ["bug", "flaky-test", "good first issue"] },
                },
                assignee: { type: "string", title: "Assignee", description: "A GitHub login." },
                priority: {
                  type: "string",
                  title: "Priority",
                  oneOf: [
                    { const: "p1", title: "Soon" },
                    { const: "p2", title: "Whenever" },
                  ],
                  default: "p2",
                },
                notify: { type: "boolean", title: "Notify the team", default: true },
              },
              required: ["assignee"],
            },
          },
        },
        {
          mcp: { server: "github", tool: "search_issues" },
          args: { query: "repo:example/payments-api flaky" },
          content: [
            { type: "text", text: "1 result: #42 TestRetry is flaky (open)" },
            {
              type: "image",
              mime_type: "image/png",
              data: chartPNG,
              size: 203,
              name: "burndown chart",
            },
            {
              type: "resource_link",
              uri: "https://github.com/example/payments-api/issues/42",
              name: "#42 TestRetry is flaky",
            },
          ],
        },
        { say: "Filed #42 and assigned it." },
      ]);
      return b.world;
    },
  },
  "agent-mcp-url": {
    description:
      "Like mcp, but the next message calls linear's list_issues, whose server sends the user to a page to connect their account first.",
    path: "/sessions/ses-backoff",
    build: () => {
      const b = workbench();
      mcpServers(b);
      b.world.replies.push([
        {
          mcp: { server: "linear", tool: "list_issues" },
          args: { team: "PAY" },
          content: [{ type: "text", text: "PAY-12 Retries hammer the webhook endpoint" }],
          elicit: {
            mode: "url",
            message: "Connect your Linear workspace to let the server read its issues.",
            url: "https://linear.app/oauth/authorize?client_id=mcp&state=abc",
          },
        },
        { say: "PAY-12 tracks the same problem." },
      ]);
      return b.world;
    },
  },
  profiles: {
    description:
      "Like workbench, with two more profiles: ses-backoff runs with Reviewer and overrides its temperature, its workspace has an AGENTS.md, and two of its model calls are recorded. For the Profiles tab, the status bar's profile, and the Context panel.",
    path: "/sessions/ses-backoff",
    build: () => profiles().world,
  },
  "chat-empty": {
    description: "A chat with no messages yet, beside the workbench's projects.",
    path: "/sessions/chat-new",
    build: () => {
      const b = workbench();
      b.chat({ id: "chat-new", title: "New chat" });
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
