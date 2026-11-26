/**
 * The compose smoke test: the real stack, from the guided setup to the home
 * conversation in Chat, which the scripted model answers. `make smoke` starts
 * the stack with compose.smoke.yaml, runs this, and takes the stack down. The
 * model is `model.ts`, a scripted endpoint. Eika delegates a shell command
 * to a worker on the home computer, which proves eikad runs it there.
 */

import { execFileSync, execSync } from "node:child_process";

import { expect, test } from "@playwright/test";

test.beforeAll(async ({ request }) => {
  // The harness waits for postgres and the sandbox image before it listens.
  await expect
    .poll(async () => (await request.get("/healthz").catch(() => null))?.status(), {
      timeout: 180_000,
      intervals: [1000],
    })
    .toBe(200);
});

test("the installed shell opens offline", async ({ page, context }) => {
  await page.goto("/");
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready;
  });
  await page.reload();
  await expect
    .poll(() => page.evaluate(() => Boolean(navigator.serviceWorker.controller)))
    .toBe(true);

  await context.setOffline(true);
  try {
    const response = await page.goto("/activity");
    expect(response?.status()).toBe(200);
    await expect(page).toHaveTitle("Eika");
    await expect(page.locator("#root > *").first()).toBeVisible();
  } finally {
    await context.setOffline(false);
  }
});

test("the harness makes the home computer before anyone sets it up", async ({ request }) => {
  // Setup has not run: this stack has a fixed API token and nothing else,
  // and the computer that holds the user's files is there regardless.
  const headers = { Authorization: `Bearer ${process.env.EIKA_SMOKE_TOKEN ?? ""}` };
  await expect
    .poll(
      async () => {
        const reply = await request.get("/api/computers", { headers });
        const body = (await reply.json()) as { computers: { home: boolean; state: string }[] };
        return body.computers.find((cx) => cx.home)?.state;
      },
      { timeout: 120_000, intervals: [1000] },
    )
    .toBe("running");
});

test("setup, then Chat on the home session, and bash on the home computer", async ({
  page,
  request,
}) => {
  const headers = { Authorization: `Bearer ${process.env.EIKA_SMOKE_TOKEN ?? ""}` };
  await page.goto("/");

  // Password.
  await page.getByLabel("Password", { exact: true }).fill("smoke-password");
  await page.getByLabel("Password again").fill("smoke-password");
  await page.getByRole("button", { name: /continue|set password/i }).click();

  // Provider: the scripted model service.
  await page.getByRole("button", { name: /^Other/ }).click();
  await page.getByLabel("Name").fill("smoke");
  await page.getByLabel("Base URL").fill("http://model:8000/v1");
  await page.getByRole("button", { name: "Save and continue" }).click();

  // Models: the one the endpoint lists.
  await page.getByRole("checkbox", { name: "smoke" }).check();
  await page.getByRole("button", { name: "Add 1 model" }).click();

  // Computer: Docker and the sandbox image are there.
  await expect(page.getByText("The harness can start containers.")).toBeVisible();
  await expect(page.getByText("is ready.")).toBeVisible();
  await page.getByRole("button", { name: "Finish" }).click();

  // The shell opens on Chat, the home session. It has no list of chats and
  // no way to start another.
  await expect(page.getByRole("heading", { level: 1, name: "Chat" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Pages" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Session" })).toBeVisible();
  await expect(page.getByRole("button", { name: /^New (home )?chat$/ })).toHaveCount(0);

  // A message to Eika, which the scripted model answers with "ok" (the
  // eika agent has no `bash`, so the model is not asked for a command).
  await page.getByLabel("Message").fill("Say hello");
  await page.getByLabel("Message").press("Enter");

  // The transcript's paragraph, not the live region that reads it out.
  await expect(page.getByRole("paragraph").filter({ hasText: /^ok$/ })).toBeVisible({
    timeout: 60_000,
  });
  // A reload lands on the same conversation.
  await page.reload();
  await expect(page.getByRole("paragraph").filter({ hasText: /^ok$/ })).toBeVisible();

  // Ask Eika to start a worker. The worker runs the probe on home.
  const computersReply = await request.get("/api/computers", { headers });
  const computers = (
    (await computersReply.json()) as {
      computers: { id: string; home: boolean; container_id?: string }[];
    }
  ).computers;
  const home = computers.find((cx) => cx.home);
  expect(home?.container_id ?? "").toMatch(/^[0-9a-f]{64}$/);
  const homeReply = await request.get("/api/home", { headers });
  expect(homeReply.status()).toBe(200);
  const homeSession = ((await homeReply.json()) as { session: { id: string } }).session;
  await expect
    .poll(
      async () => {
        const reply = await request.get(`/api/sessions/${homeSession.id}/run`, { headers });
        return reply.ok() && !((await reply.json()) as { active: boolean }).active;
      },
      { timeout: 60_000, intervals: [1000] },
    )
    .toBe(true);
  await page.getByLabel("Message").fill("Run the probe");
  await page.getByLabel("Message").press("Enter");

  // The command prints the hostname Docker gave the container, the first
  // twelve characters of its id, and its working directory. Only eikad running
  // it in the home computer's container prints both: the harness has another
  // hostname and another directory.
  const expected = `The command printed smoke-ok ${(home?.container_id ?? "").slice(0, 12)} /home/eika.`;
  await expect
    .poll(
      async () => {
        const sessions = await request.get("/api/sessions", { headers });
        if (!sessions.ok()) return false;
        const rows = (
          (await sessions.json()) as {
            sessions: {
              id: string;
              kind: string;
              name?: string;
              parent_session_id?: string;
              computer_id?: string;
            }[];
          }
        ).sessions;
        const worker = rows.find(
          (s) =>
            s.kind === "subagent" &&
            s.name === "smoke-worker" &&
            s.parent_session_id === homeSession.id &&
            s.computer_id === home?.id,
        );
        if (!worker) return false;
        const path = await request.get(`/api/sessions/${worker.id}/path`, { headers });
        if (!path.ok()) return false;
        const body = (await path.json()) as { messages: { role: string; content?: string }[] };
        return body.messages.some((m) => m.role === "assistant" && m.content === expected);
      },
      { timeout: 60_000, intervals: [1000] },
    )
    .toBe(true);
  await expect
    .poll(
      async () => {
        const reply = await request.get(`/api/sessions/${homeSession.id}/run`, { headers });
        return reply.ok() && !((await reply.json()) as { active: boolean }).active;
      },
      { timeout: 60_000, intervals: [1000] },
    )
    .toBe(true);
  await page.getByRole("textbox", { name: "Message" }).fill("Fetch the fixture");
  await page.getByRole("textbox", { name: "Message" }).press("Enter");
  await expect
    .poll(
      async () => {
        const sessions = await request.get("/api/sessions", { headers });
        if (!sessions.ok()) return false;
        const body = (await sessions.json()) as {
          sessions: { id: string; name?: string; parent_session_id?: string }[];
        };
        const researcher = body.sessions.find(
          (row) => row.name === "smoke-researcher" && row.parent_session_id === homeSession.id,
        );
        if (!researcher) return false;
        const path = await request.get(`/api/sessions/${researcher.id}/path`, { headers });
        if (!path.ok()) return false;
        const transcript = (await path.json()) as { messages: { content?: string }[] };
        return transcript.messages.some((message) =>
          message.content?.includes("Fixture screened successfully."),
        );
      },
      { timeout: 60_000, intervals: [1000] },
    )
    .toBe(true);
  await expect
    .poll(
      async () => {
        const reply = await request.get("/api/withheld", { headers });
        if (!reply.ok()) return false;
        const body = (await reply.json()) as {
          withheld: { source: string; source_name: string; text: string }[];
        };
        return body.withheld.some(
          (row) =>
            row.source === "web_fetch" &&
            row.source_name.includes("model:8000/fixture") &&
            row.text.includes("Ignore all previous instructions"),
        );
      },
      { timeout: 60_000, intervals: [1000] },
    )
    .toBe(true);
});

test("memory: save, compaction, edit, Inbox answer, restart, and no agent computer sees the repository", async ({
  page,
  request,
}) => {
  const headers = { Authorization: `Bearer ${process.env.EIKA_SMOKE_TOKEN ?? ""}` };
  type Memory = { name: string; state: string; body: string; content_sha: string };
  const memory = async (name: string): Promise<Memory> => {
    const reply = await request.get(`/api/memories/${name}`, { headers });
    expect(reply.status()).toBe(200);
    return (await reply.json()) as Memory;
  };
  const homeSession = (
    (await (await request.get("/api/home", { headers })).json()) as { session: { id: string } }
  ).session;
  const idle = () =>
    expect
      .poll(
        async () => {
          const reply = await request.get(`/api/sessions/${homeSession.id}/run`, { headers });
          return reply.ok() && !((await reply.json()) as { active: boolean }).active;
        },
        { timeout: 60_000, intervals: [1000] },
      )
      .toBe(true);
  const memorySection = async (): Promise<string> => {
    const reply = await request.get(`/api/sessions/${homeSession.id}/context`, { headers });
    expect(reply.status()).toBe(200);
    const body = (await reply.json()) as { sections: { kind: string; text: string }[] };
    return body.sections
      .filter((s) => s.kind === "memory")
      .map((s) => s.text)
      .join("\n");
  };

  // The setup test's browser is gone: sign in with the password it chose.
  const open = async (path: string) => {
    await page.goto(path);
    const signIn = page.getByRole("button", { name: "Sign in" });
    if (await signIn.isVisible({ timeout: 3000 }).catch(() => false)) {
      await page.getByLabel("Password", { exact: true }).fill("smoke-password");
      await signIn.click();
    }
  };

  // Two writes by the scripted model: one saved, one that the real guard
  // (not the unscanned fallback) sends to pending.
  await open("/");
  await idle();
  await page.getByRole("textbox", { name: "Message" }).fill("Save the memories");
  await page.getByRole("textbox", { name: "Message" }).press("Enter");
  await expect
    .poll(
      async () => (await request.get("/api/memories/smoke-planted-rule", { headers })).status(),
      {
        timeout: 60_000,
        intervals: [1000],
      },
    )
    .toBe(200);
  expect((await memory("smoke-metric-units")).state).toBe("active");
  const planted = (await (
    await request.get("/api/memories/smoke-planted-rule", { headers })
  ).json()) as { state: string; screen?: { max_score: number; unscanned: boolean } };
  expect(planted.state).toBe("pending");
  expect(planted.screen?.unscanned).toBe(false);
  await idle();

  // A small window, so that the long turn above is more than a compaction keeps.
  const models = (
    (await (await request.get("/api/models", { headers })).json()) as {
      models: { id: string; name: string }[];
    }
  ).models;
  const smoke = models.find((m) => m.name === "smoke");
  const patched = await request.patch(`/api/models/${smoke?.id ?? ""}`, {
    headers,
    data: { context_window: 32000, max_output: 4000 },
  });
  expect(patched.status()).toBe(200);

  // A compaction rebuilds the home preamble: it lists the active memory and
  // never the pending one.
  const compact = await request.post(`/api/sessions/${homeSession.id}/compact`, {
    headers,
    data: {},
  });
  expect(compact.status(), await compact.text()).toBe(202);
  await idle();
  const section = await memorySection();
  expect(section).toContain("smoke-metric-units");
  expect(section).not.toContain("smoke-planted-rule");

  // The user edits the active memory in the Memory page.
  await open("/memory/smoke-metric-units");
  await page
    .getByLabel("Body", { exact: true })
    .fill("The user wants metric units, always (2026-11-22).");
  await page.getByRole("button", { name: "Save memory" }).click();
  await expect(page.getByText("Saved.")).toBeVisible();
  expect((await memory("smoke-metric-units")).body).toContain("always");

  // The user answers the pending one in the Inbox.
  await open("/inbox");
  await page
    .getByRole("article", { name: "smoke-planted-rule" })
    .getByRole("button", { name: "Reject" })
    .click();
  await expect(page.getByRole("article", { name: "smoke-planted-rule" })).toHaveCount(0);
  expect((await memory("smoke-planted-rule")).state).toBe("archived");

  // The repository and the states survive a restart of the harness.
  const before = [await memory("smoke-metric-units"), await memory("smoke-planted-rule")];
  execSync(`${process.env.EIKA_SMOKE_COMPOSE ?? "false"} restart eika`, {
    cwd: "..",
    stdio: "inherit",
    timeout: 120_000,
  });
  await expect
    .poll(
      async () =>
        (
          await request.get("/api/memories/smoke-planted-rule", { headers }).catch(() => null)
        )?.status(),
      {
        timeout: 180_000,
        intervals: [2000],
      },
    )
    .toBe(200);
  for (const was of before) {
    const now = await memory(was.name);
    expect(now.state).toBe(was.state);
    expect(now.content_sha).toBe(was.content_sha);
    expect(now.body).toBe(was.body);
  }

  // No agent computer can read the memory mount. A worker on home looks for it
  // and finds nothing, and no computer container mounts the repository.
  const computers = (
    (await (await request.get("/api/computers", { headers })).json()) as {
      computers: { id: string; home: boolean }[];
    }
  ).computers;
  const home = computers.find((cx) => cx.home);
  await idle();
  await open("/");
  await page.getByRole("textbox", { name: "Message" }).fill("Check the memory mount");
  await page.getByRole("textbox", { name: "Message" }).press("Enter");
  await expect
    .poll(
      async () => {
        const rows = (
          (await (await request.get("/api/sessions", { headers })).json()) as {
            sessions: { id: string; name?: string; computer_id?: string }[];
          }
        ).sessions;
        const worker = rows.find((s) => s.name === "smoke-mount-worker");
        if (!worker || worker.computer_id !== home?.id) return false;
        const path = await request.get(`/api/sessions/${worker.id}/path`, { headers });
        if (!path.ok()) return false;
        const body = (await path.json()) as { messages: { role: string; content?: string }[] };
        return body.messages.some(
          (m) => m.role === "assistant" && m.content === "The command printed mount-isolated 1 0.",
        );
      },
      { timeout: 90_000, intervals: [1000] },
    )
    .toBe(true);
  const docker = (args: string): string[] =>
    execSync(`docker ${args}`, { encoding: "utf8" }).split("\n").filter(Boolean);
  const agentComputers = [
    ...docker("ps -aq --filter label=eika.computer --filter network=eika_smoke_sandbox"),
    ...docker("ps -aq --filter label=eika.computer --filter network=eika_smoke_sandbox_internal"),
  ];
  const mounts = docker(
    `inspect -f '{{.Config.Labels}} {{range .Mounts}}{{.Destination}} {{end}}' ${agentComputers.join(" ")}`,
  );
  expect(mounts.length).toBeGreaterThan(0);
  expect(mounts.some((line) => line.includes("eika.role:memory"))).toBe(true);
  for (const line of mounts.filter((l) => !l.includes("eika.role:memory"))) {
    expect(line).not.toContain("/var/lib/eika/memory");
  }
});

test("skills: install, use, isolate, and survive restart", async ({ page, request }) => {
  const headers = { Authorization: `Bearer ${process.env.EIKA_SMOKE_TOKEN ?? ""}` };
  const zip = execFileSync("python3", [
    "-c",
    `
import io, pathlib, sys, zipfile
root = pathlib.Path('../internal/skill/testdata/invoice-totals')
buf = io.BytesIO()
with zipfile.ZipFile(buf, 'w', zipfile.ZIP_DEFLATED) as archive:
    for path in sorted(root.rglob('*')):
        if path.is_file():
            archive.write(path, pathlib.Path('invoice-totals') / path.relative_to(root))
sys.stdout.buffer.write(buf.getvalue())
`,
  ]);
  await page.goto("/skills");
  const signIn = page.getByRole("button", { name: "Sign in" });
  if (await signIn.isVisible({ timeout: 3000 }).catch(() => false)) {
    await page.getByLabel("Password", { exact: true }).fill("smoke-password");
    await signIn.click();
  }
  await page
    .getByLabel("Skill zip")
    .setInputFiles({ name: "invoice-totals.zip", mimeType: "application/zip", buffer: zip });
  await page.getByRole("link", { name: "Review invoice-totals" }).click();
  await expect(page.getByRole("region", { name: "SKILL.md" })).toContainText("Run `python3");
  await expect(page.getByRole("region", { name: "Files" })).toContainText("scripts/totals.py");
  await page.getByRole("button", { name: "Enable", exact: true }).click();
  await expect(page.getByRole("button", { name: "Disable", exact: true })).toBeVisible();

  const home = (
    (await (await request.get("/api/home", { headers })).json()) as { session: { id: string } }
  ).session;
  await expect
    .poll(
      async () => {
        const reply = await request.get(`/api/sessions/${home.id}/run`, { headers });
        return reply.ok() && !((await reply.json()) as { active: boolean }).active;
      },
      { timeout: 60_000, intervals: [1000] },
    )
    .toBe(true);
  await page.goto("/");
  await page.getByLabel("Message", { exact: true }).fill("Total the invoice");
  await page.getByLabel("Message", { exact: true }).press("Enter");
  await expect
    .poll(
      async () => {
        const reply = await request.get("/api/sessions", { headers });
        if (!reply.ok()) return false;
        const rows = (
          (await reply.json()) as {
            sessions: { id: string; name?: string; parent_session_id?: string }[];
          }
        ).sessions;
        const worker = rows.find(
          (row) => row.name === "smoke-skill-worker" && row.parent_session_id === home.id,
        );
        if (!worker) return false;
        const path = await request.get(`/api/sessions/${worker.id}/path`, { headers });
        if (!path.ok()) return false;
        const messages = ((await path.json()) as { messages: { role: string; content?: string }[] })
          .messages;
        return messages.some(
          (message) =>
            message.role === "assistant" &&
            message.content?.includes("Acme: net 20.00, vat 3.80, total 23.80") &&
            message.content.includes("GRAND TOTAL 23.80") &&
            message.content.includes("skills-read-only") &&
            message.content.includes("private-hidden"),
        );
      },
      { timeout: 90_000, intervals: [1000] },
    )
    .toBe(true);

  const before = (await (await request.get("/api/skills/invoice-totals", { headers })).json()) as {
    enabled: boolean;
    sha: string;
    source: string;
  };
  expect(before.enabled).toBe(true);
  execSync(`${process.env.EIKA_SMOKE_COMPOSE ?? "false"} restart eika`, {
    cwd: "..",
    stdio: "inherit",
    timeout: 120_000,
  });
  await expect
    .poll(
      async () =>
        (await request.get("/api/skills/invoice-totals", { headers }).catch(() => null))?.status(),
      { timeout: 180_000, intervals: [2000] },
    )
    .toBe(200);
  const after = (await (
    await request.get("/api/skills/invoice-totals", { headers })
  ).json()) as typeof before;
  expect(after.enabled).toBe(true);
  expect(after.sha).toBe(before.sha);
  expect(after.source).toBe(before.source);

  const ids = execSync(
    "docker ps -aq --filter label=eika.computer --filter network=eika_smoke_sandbox; docker ps -aq --filter label=eika.computer --filter network=eika_smoke_sandbox_internal",
    { encoding: "utf8" },
  )
    .trim()
    .split(/\s+/)
    .filter(Boolean);
  expect(ids.length).toBeGreaterThan(0);
  const containers = JSON.parse(
    execFileSync("docker", ["inspect", ...ids], { encoding: "utf8" }),
  ) as {
    Config: { Labels: Record<string, string> };
    Mounts: { Destination: string; Source: string; RW: boolean }[];
  }[];
  const agents = containers.filter(
    (container) => !["memory", "skills"].includes(container.Config.Labels["eika.role"] ?? ""),
  );
  expect(agents.length).toBeGreaterThan(0);
  const memory = containers.find((container) => container.Config.Labels["eika.role"] === "memory");
  expect(memory).toBeDefined();
  const coreID = execSync(`${process.env.EIKA_SMOKE_COMPOSE ?? "false"} ps -q eika`, {
    cwd: "..",
    encoding: "utf8",
  }).trim();
  const core = JSON.parse(execFileSync("docker", ["inspect", coreID], { encoding: "utf8" })) as {
    Mounts: { Destination: string; Source: string }[];
  }[];
  const data = core[0]?.Mounts.find((mount) => mount.Destination === "/var/lib/eika");
  expect(data).toBeDefined();
  const privateSources = [data?.Source, ...(memory?.Mounts.map((mount) => mount.Source) ?? [])];
  for (const container of agents) {
    expect(container.Mounts.some((mount) => privateSources.includes(mount.Source))).toBe(false);
    expect(container.Mounts.find((mount) => mount.Destination === "/skills")?.RW).toBe(false);
    expect(
      container.Mounts.some(
        (mount) =>
          mount.Destination === "/var/lib/eika" || mount.Destination.startsWith("/var/lib/eika/"),
      ),
    ).toBe(false);
  }
});

test("scheduler: approval, execution and missed occurrence", async ({ page, request }) => {
  test.setTimeout(240_000);
  const headers = { Authorization: `Bearer ${process.env.EIKA_SMOKE_TOKEN ?? ""}` };
  await page.goto("/");
  const signIn = page.getByRole("button", { name: "Sign in" });
  if (await signIn.isVisible({ timeout: 3000 }).catch(() => false)) {
    await page.getByLabel("Password", { exact: true }).fill("smoke-password");
    await signIn.click();
  }
  await page.getByLabel("Message", { exact: true }).fill("Create smoke routine");
  await page.getByLabel("Message", { exact: true }).press("Enter");
  type Routine = { id: string; name: string; enabled: boolean; approved_at: string | null };
  let agentRoutine: Routine | undefined;
  await expect
    .poll(
      async () => {
        const response = await request.get("/api/routines", { headers });
        const data = (await response.json()) as { routines: Routine[] };
        agentRoutine = data.routines.find((r) => r.name === "agent smoke");
        return !!agentRoutine;
      },
      { timeout: 60_000 },
    )
    .toBe(true);
  expect(agentRoutine?.enabled).toBe(false);
  expect(agentRoutine?.approved_at).toBeNull();
  const approved = await request.post(`/api/routines/${agentRoutine?.id ?? ""}/approve`, {
    headers,
  });
  expect(approved.ok()).toBe(true);
  expect(((await approved.json()) as Routine).enabled).toBe(true);

  const due = new Date(Date.now() + 20_000).toISOString();
  const created = await request.post("/api/routines", {
    headers,
    data: {
      name: "smoke missed",
      schedule_kind: "at",
      schedule_expr: due,
      timezone: "UTC",
      agent: "eika",
      prompt: "Routine smoke result",
      enabled: false,
    },
  });
  expect(created.status()).toBe(201);
  const routine = (await created.json()) as Routine;
  expect(routine.approved_at).not.toBeNull();
  expect((await request.post(`/api/routines/${routine.id}/approve`, { headers })).ok()).toBe(true);
  const compose = process.env.EIKA_SMOKE_COMPOSE ?? "false";
  execSync(`${compose} stop eika`, { cwd: "..", stdio: "inherit", timeout: 120_000 });
  await new Promise((resolve) =>
    setTimeout(resolve, Math.max(0, Date.parse(due) - Date.now() + 2000)),
  );
  execSync(`${compose} start eika`, { cwd: "..", stdio: "inherit", timeout: 120_000 });
  let runs: { session_id: string; outcome: string }[] = [];
  await expect
    .poll(
      async () => {
        const response = await request
          .get(`/api/routines/${routine.id}/runs`, { headers })
          .catch(() => null);
        if (!response?.ok()) return false;
        runs = ((await response.json()) as { runs: typeof runs }).runs;
        return runs.length === 1 && runs[0]?.outcome === "done";
      },
      { timeout: 180_000, intervals: [2000] },
    )
    .toBe(true);
  const sessions = (await (await request.get("/api/sessions", { headers })).json()) as {
    sessions: { id: string; kind: string }[];
  };
  expect(sessions.sessions.find((s) => s.id === runs[0]?.session_id)?.kind).toBe("routine");
  const path = await request.get(`/api/sessions/${runs[0]?.session_id ?? ""}/path`, { headers });
  const messages = (
    (await path.json()) as {
      messages: { role: string; content?: string; tool_calls?: { name: string }[] }[];
    }
  ).messages;
  expect(
    messages.some(
      (m) => m.role === "assistant" && m.content?.includes("Routine smoke result complete"),
    ),
  ).toBe(true);
  expect(messages.some((m) => m.tool_calls?.some((c) => c.name === "ask_user"))).toBe(false);
  const home = (
    (await (await request.get("/api/home", { headers })).json()) as { session: { id: string } }
  ).session;
  await expect
    .poll(
      async () => {
        const response = await request.get(`/api/sessions/${home.id}/path`, { headers });
        return JSON.stringify(await response.json()).includes("Routine smoke result complete");
      },
      { timeout: 60_000 },
    )
    .toBe(true);
});
