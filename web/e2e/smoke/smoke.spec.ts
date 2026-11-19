/**
 * The compose smoke test: the real stack, from the guided setup to the home
 * conversation in Chat, which the scripted model answers. `make smoke` starts
 * the stack with compose.smoke.yaml, runs this, and takes the stack down. The
 * model is `model.ts`, a scripted endpoint. Eika delegates a shell command
 * to a worker on the home computer, which proves eikad runs it there.
 */

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
});
