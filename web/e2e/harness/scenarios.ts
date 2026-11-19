/**
 * Named starting states for the UI. Each builds a fresh World; `path` is where
 * the scenario is most useful to open. Add a scenario when a screen needs data
 * no existing one has, and keep ids stable: specs and agents address them.
 */

import type { Message, Project, Run } from "../../src/api/types.ts";
import { noSettings, syncSession } from "./profiles.ts";
import { emptyWorld, minutesAgo, WorldBuilder } from "./world.ts";
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
 * homeWorld is the home conversation with two named subagents: `plumber`,
 * running, and `repair`, idle with a report. Their ids are fixed.
 */
function homeWorld(): WorldBuilder {
  const b = configured();
  b.world.settings.settings.model_classes = { main: "gpt-5", fast: "gpt-5-mini" };
  const home = b.home();
  b.conversation(home, [
    { role: "user", content: "Find a plumber." },
    { role: "assistant", content: "I will look for one." },
  ]);
  const running = b.subagent(home, {
    id: "subagent-plumber",
    name: "plumber",
    agent: "researcher",
    state: "running",
    brief: "Find a plumber.",
  });
  b.conversation(running, [{ role: "assistant", content: "Checking local options." }]);
  const computer = b.computer({ id: "cx-repair", name: "repair" });
  b.subagent(home, {
    computer_id: computer.id,
    id: "subagent-repair",
    name: "repair",
    agent: "worker",
    state: "idle",
    brief: "Inspect the repair.",
    result: "The repair is complete.",
  });
  return b;
}

/**
 * activity adds to the home world what the Activity page shows: two projects,
 * and subagents in every state. The running and the waiting ones have a run
 * going, which Cancel stops. Their ids are `subagent-` and their name.
 */
function activity(): WorldBuilder {
  const b = homeWorld();
  const w = b.world;
  // Chat attaches images when the model takes them, which a spec of leaving Chat mid-draft needs.
  for (const m of w.models) m.image_input = m.name === "gpt-5";
  const home = b.home();
  const project = (slug: string, name: string): Project => ({
    id: `prj-${slug}`,
    slug,
    name,
    description: "",
    status: "active",
    directory: `/home/eika/projects/${slug}`,
    created_at: minutesAgo(600),
    updated_at: minutesAgo(600),
  });
  w.projects.push(project("garden", "Garden"), project("kitchen", "Kitchen"));
  const known = (id: string) => {
    const row = w.sessions.find((s) => s.id === id);
    if (row === undefined) throw new Error(`the home world has no session ${id}`);
    return row;
  };
  const plumber = Object.assign(known("subagent-plumber"), {
    project_id: "prj-kitchen",
    last_active_at: minutesAgo(2),
  });
  Object.assign(known("subagent-repair"), {
    project_id: "prj-kitchen",
    last_active_at: minutesAgo(45),
  });

  const waiting = b.subagent(home, {
    id: "subagent-permit",
    name: "permit",
    agent: "researcher",
    state: "waiting",
    waiting_reason: "the user's answer about which council applies",
    project_id: "prj-kitchen",
    brief: "Find out whether the kitchen extension needs a permit.\nCheck the council's rules.",
    last_active_at: minutesAgo(9),
  });
  b.conversation(waiting, [
    { role: "user", content: "Find out whether the kitchen extension needs a permit." },
    { role: "assistant", content: "The rules differ by council, so I need to know which applies." },
  ]);
  const slow = b.subagent(home, {
    id: "subagent-quotes",
    name: "quotes",
    agent: "worker",
    state: "waiting",
    waiting_reason: "a slow bash call, running for 4 minutes",
    brief: "Collect three quotes for the tiling and write them to quotes.md.",
    last_active_at: minutesAgo(5),
  });
  b.conversation(slow, [
    { role: "user", content: "Collect three quotes for the tiling." },
    {
      role: "assistant",
      tool_calls: [
        { id: "call-quotes", name: "bash", arguments: { command: "./fetch-quotes.sh" } },
      ],
    },
  ]);
  const seedlings = b.subagent(home, {
    id: "subagent-seedlings",
    name: "seedlings",
    agent: "researcher",
    state: "idle",
    project_id: "prj-garden",
    brief: "Which tomato seedlings do well in a cold greenhouse?",
    result:
      "Three varieties suit a cold greenhouse: Latah, Stupice and Siberian.\n\nOutputs:\n- notes/tomatoes.md\n\nFollow-ups:\n- Check sowing dates for the region",
    last_active_at: minutesAgo(130),
  });
  b.conversation(seedlings, [
    { role: "user", content: "Which tomato seedlings do well in a cold greenhouse?" },
    { role: "assistant", content: "Latah, Stupice and Siberian are the usual answers." },
  ]);
  const tiling = b.subagent(home, {
    id: "subagent-tiling",
    name: "tiling",
    agent: "worker",
    state: "failed",
    project_id: "prj-kitchen",
    brief: "Measure the splashback and list the tiles it needs.",
    last_active_at: minutesAgo(70),
  });
  b.conversation(tiling, [
    { role: "user", content: "Measure the splashback and list the tiles it needs." },
    { role: "assistant", content: "The measurements file is missing, so I cannot continue." },
  ]);
  const paint = b.subagent(home, {
    id: "subagent-paint",
    name: "paint",
    agent: "researcher",
    state: "cancelled",
    project_id: "prj-garden",
    brief: "Compare fence paints.",
    last_active_at: minutesAgo(300),
  });
  b.conversation(paint, [{ role: "user", content: "Compare fence paints." }]);

  for (const child of [plumber, waiting, slow]) {
    const run: Run = {
      id: `run-${child.name ?? ""}`,
      session_id: child.id,
      state: "running",
      started_at: minutesAgo(10),
    };
    w.runs[run.id] = run;
    w.activeRuns[child.id] = run.id;
  }
  return b;
}

/**
 * workbench builds the richest state: computers in several states, and
 * a transcript that uses every kind of entry, on the home session Chat shows
 * and on `ses-backoff`, a session of the computer `cx-retries`. Their ids are fixed.
 */
function workbench(): WorldBuilder {
  const b = configured();
  b.computer({ name: "dark-mode", state: "stopped" });
  const cx = b.computer({ id: "cx-retries", name: "fix-retries" });
  b.computer({ name: "upgrade-go", state: "creating" });
  const older = b.session(cx, { title: "Investigate flaky test" });
  b.conversation(older, [
    { role: "user", content: "Why does TestRetry fail sometimes?" },
    {
      role: "assistant",
      content: "It depends on wall-clock time; I'll switch it to a fake clock.",
    },
  ]);
  const main = b.session(cx, { id: "ses-backoff", title: "Add exponential backoff to retries" });
  const backoff: Message[] = [
    {
      role: "user",
      content:
        "The webhook client retries immediately on failure. Add exponential backoff with jitter, capped at 30s.",
      context: "<context>\ntime: Sat 2026-03-14 12:00 +00:00 (UTC)\n</context>",
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
  ];
  b.conversation(main, backoff);
  // The conversation Chat shows is the home session, which tells the same story.
  b.conversation(b.home(), backoff);
  return b;
}

/**
 * profiles adds two profiles to a workbench, puts ses-backoff on one with an
 * override of its own, and records two of its model calls: one when only
 * its first message had been sent, and one at its head. Its endpoint counts
 * tokens the way `counting` says: by rendering requests (chat), by tokenising
 * text (text), or not at all (none).
 */
function profiles(counting: "chat" | "text" | "none" = "chat"): WorldBuilder {
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
  const cx = w.computers.find((x) => x.id === "cx-retries");
  const main = w.sessions.find((x) => x.id === "ses-backoff");
  if (!cx || !main) throw new Error("the workbench has no ses-backoff");
  main.profile_id = reviewer.id;
  w.overrides[main.id] = { ...noSettings(), sampling: { temperature: 0.7 } };
  syncSession(w, main);
  // An endpoint that renders and tokenises requests (llama.cpp, vLLM), so the
  // context view counts every part exactly and predicts the next request
  // exactly. A scenario that wants another way of counting says so.
  for (const p of w.providers) {
    p.capabilities = { ...p.capabilities, counting, cache_metrics: "reported" };
  }
  const head = main.head_entry_id;
  const first = (w.entries[main.id] ?? [])[0];
  if (first) main.head_entry_id = first.id;
  b.call(main, 40, 212);
  main.head_entry_id = head;
  b.call(main, 12, 318);
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
          text: "Allowed directories: [ '/home/eika' ]",
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
    description: "Signed in and set up, but no providers or models.",
    path: "/",
    build: () => emptyWorld(),
  },
  configured: {
    description: "One provider with two models.",
    path: "/",
    build: () => configured().world,
  },
  workbench: {
    description:
      "Computers (running/stopped/creating), and session ses-backoff with a full transcript. Sending a message streams a mock reply.",
    path: "/",
    build: () => workbench().world,
  },
  "agent-tools": {
    description:
      "Like workbench, but the next message plays a reply that runs bash (streamed output), edits a file, and answers.",
    path: "/",
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
    path: "/",
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
  "agent-images": {
    description:
      "Like workbench, but gpt-5 accepts images, so the composer attaches them and the transcript shows them.",
    path: "/",
    build: () => {
      const b = workbench();
      for (const m of b.world.models) m.image_input = m.name === "gpt-5";
      b.world.replies.push([{ say: "The left half is teal and the right half is orange." }]);
      return b.world;
    },
  },
  "agent-question": {
    description:
      "Like workbench, but the next message makes the agent ask a multiple-choice question and wait.",
    path: "/",
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
    path: "/",
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
    path: "/",
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
  "agent-run-limit": {
    description:
      "Like workbench, but the next message's run reaches its limit of model calls and ends cleanly: what it did is kept and the transcript says why it stopped.",
    path: "/",
    build: () => {
      const b = workbench();
      b.world.replies.push([{ say: "Still working through the files." }, { cutOff: "max_calls" }]);
      return b.world;
    },
  },
  "agent-running": {
    description:
      "Like workbench, but the next message starts a run that never finishes (for the running state and abort).",
    path: "/",
    build: () => {
      const b = workbench();
      b.world.replies.push([{ say: "Working on it, this will take a while…" }, { hang: true }]);
      return b.world;
    },
  },
  providers: {
    description:
      "Four providers: OpenAI with a stored key and two models, a local vLLM that needs no key and states its cache, a llama.cpp bench with two slots whose cache the probe cannot tell, and OpenRouter whose key is missing and whose last probe failed. For the Models tab, ProviderForm, ModelPicker, and the endpoint settings.",
    path: "/",
    build: () => {
      const b = configured();
      b.provider({
        name: "Local vLLM",
        base_url: "http://localhost:8000/v1",
        api_key_set: false,
        api_key_hint: undefined,
        backend: "vllm",
        slots: 1,
        kv: "shared",
        kv_tokens: 134736,
        capabilities: {
          build: "0.11.0",
          window: 40960,
          counting: "chat",
          cache_metrics: "unknown",
          slot_hints: false,
        },
      });
      // What the probe says about llama.cpp b11942 with two slots: the window
      // of one request, and no word on whether the cache is unified.
      b.world.probeFinds = {
        backend: "llamacpp",
        slots: 2,
        kv: "unknown",
        api: "responses",
        capabilities: {
          build: "b11942-338fce651",
          window: 131072,
          counting: "chat",
          cache_metrics: "reported",
          slot_hints: true,
          responses: {
            verdict: "passed",
            model: "qwen3.8-27b",
            build: "b11942-338fce651",
            checked_at: "2026-10-31T12:00:00Z",
            reasoning: true,
            cache_metrics: true,
            counted: true,
            drift: 0,
          },
        },
      };
      b.provider({
        name: "Bench",
        base_url: "http://10.0.0.2:8091/v1",
        api_key_hint: undefined,
        backend: "llamacpp",
        slots: 2,
        kv: "unknown",
        api: "responses",
        capabilities: b.world.probeFinds.capabilities,
      });
      const router = b.provider({
        name: "OpenRouter",
        base_url: "https://openrouter.ai/api/v1",
        api_key_set: false,
        api_key_hint: undefined,
        probe_error: "the endpoint could not be used: 401 Unauthorized: No auth credentials found",
      });
      b.model(router, { name: "claude-sonnet", model: "anthropic/claude-sonnet-4.5" });
      return b.world;
    },
  },
  classes: {
    description:
      "The workbench with a llama.cpp bench (one chat model), a decision model on a server of its own, and the OpenAI models. The classes are set: deep is gpt-5, main is the bench's model, decision is d3-flash, and fast is not set, so it falls back to main on the same pool. For the model classes in Settings, General.",
    path: "/",
    build: () => {
      const b = workbench();
      const bench = b.provider({
        name: "Bench",
        base_url: "http://10.0.0.2:8091/v1",
        api_key_hint: undefined,
        backend: "llamacpp",
        slots: 2,
        kv: "unknown",
      });
      b.model(bench, { name: "bench-chat", model: "qwen3.8-27b", context_window: 131072 });
      const decider = b.provider({
        name: "Decider",
        base_url: "http://10.0.0.3:9941/v1",
        api_key_set: false,
        api_key_hint: undefined,
        backend: "llamacpp",
        slots: 2,
        kv: "unknown",
      });
      b.model(decider, { name: "d3-flash", kind: "decision", context_window: 32768 });
      b.world.settings.settings.model_classes = {
        deep: "gpt-5",
        main: "bench-chat",
        decision: "d3-flash",
      };
      return b.world;
    },
  },
  "bench-window": {
    description:
      "A llama.cpp bench that serves 131,072 tokens per request, with one model whose context window is set to 200,000 (Settings warns) and one set within it (no warning). For the Models tab.",
    path: "/",
    build: () => {
      const b = configured();
      const bench = b.provider({
        name: "Bench",
        base_url: "http://10.0.0.2:8091/v1",
        api_key_hint: undefined,
        backend: "llamacpp",
        slots: 2,
        kv: "unknown",
        capabilities: {
          build: "b11942-338fce651",
          window: 131072,
          counting: "chat",
          cache_metrics: "reported",
          slot_hints: true,
        },
      });
      b.model(bench, {
        name: "qwen-long",
        model: "qwen3.8-27b",
        context_window: 200000,
        max_output: 16384,
      });
      b.model(bench, {
        name: "qwen-short",
        model: "qwen3.8-27b",
        context_window: 65536,
        max_output: 8192,
      });
      return b.world;
    },
  },
  "agent-web": {
    description:
      "Like workbench with an abandoned branch in the session tree; the next message plays a reply that uses web_search, web_fetch, bash to read, search, and write files, and a tool with no renderer of its own. Expand a card by its summary to see its body.",
    path: "/",
    build: () => {
      const b = workbench();
      // An abandoned first answer, so the session tree has a branch.
      const entries = b.world.entries[b.home().id] ?? [];
      const first = entries[0];
      if (first) {
        entries.push({
          id: "cccccccccccccccccccc",
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
          tool: "read_note",
          args: { title: "Backoff" },
          result: '{"status":"done"}',
        },
        { say: "Full jitter it is: the cap stays at 30s." },
      ]);
      return b.world;
    },
  },
  chat: {
    description:
      "Like workbench, but the next message plays a reply that searches the web and answers.",
    path: "/",
    build: () => {
      const b = workbench();
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
    path: "/",
    build: () => {
      const b = workbench();
      mcpServers(b);
      return b.world;
    },
  },
  "agent-mcp": {
    description:
      "Like mcp, but the next message calls github's create_issue, which asks for input in a form first, then search_issues, which answers with text and an image.",
    path: "/",
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
    path: "/",
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
      "Like workbench, with two more profiles: ses-backoff, which no page opens, runs with Reviewer and overrides its temperature, and two of its model calls are recorded. The legacy profile data, for the context meter's tests.",
    path: "/",
    build: () => profiles().world,
  },
  "context-estimated": {
    description:
      "Like profiles, but the endpoint has no tokenizer: every part of a request is estimated from bytes and marked so, the next request's size is an estimate, and no remainder can be stated for it.",
    path: "/",
    build: () => profiles("none").world,
  },
  "home-world": {
    description: "The home conversation and named subagents in different states.",
    path: "/",
    build: () => homeWorld().world,
  },
  activity: {
    description:
      "Activity: subagents of two projects in every state, a waiting one with each reason, and runs to cancel.",
    path: "/activity",
    build: () => activity().world,
  },
  projects: {
    description: "Projects with subagents and saved instructions.",
    path: "/projects",
    build: () => {
      const b = activity();
      b.world.projectInstructions["prj-kitchen"] = "Use metric measurements.";
      return b.world;
    },
  },
  "home-empty": {
    description: "Eika's conversation before any message: the home session, empty.",
    path: "/",
    build: () => configured().world,
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
