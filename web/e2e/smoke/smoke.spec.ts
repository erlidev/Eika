/**
 * The compose smoke test: the real stack, from the guided setup to a chat
 * the scripted model answers. `make smoke` starts the stack with
 * compose.smoke.yaml, runs this, and takes the stack down. The model is
 * `model.ts`, a scripted endpoint. A plain chat reaches no sandbox; a chat on
 * the home computer runs `bash` in its container.
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

test("setup, a chat, and a chat on the home computer", async ({ page, request }) => {
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

  // A chat, which the scripted model answers with "ok".
  await page
    .getByRole("navigation", { name: "Chats" })
    .getByRole("button", { name: "New chat" })
    .click();
  await page.getByLabel("Message").fill("Say hello");
  await page.getByLabel("Message").press("Enter");

  // The transcript's paragraph, not the live region that reads it out.
  await expect(page.getByRole("paragraph").filter({ hasText: /^ok$/ })).toBeVisible({
    timeout: 60_000,
  });

  // A chat on the home computer, whose model asks for a shell command.
  await page
    .getByRole("navigation", { name: "Chats" })
    .getByRole("button", { name: "New home chat" })
    .click();
  await expect(
    page.getByRole("region", { name: "Session" }).locator("header").getByText("Home computer"),
  ).toBeVisible();
  await page.getByLabel("Message").fill("Run the probe");
  await page.getByLabel("Message").press("Enter");

  // The command prints the hostname Docker gave the container, the first
  // twelve characters of its id, and its working directory. Only eikad running
  // it in the home computer's container prints both: the harness has another
  // hostname and another directory.
  const reply = await request.get("/api/computers", { headers });
  const computers = (
    (await reply.json()) as { computers: { home: boolean; container_id?: string }[] }
  ).computers;
  const containerId = computers.find((cx) => cx.home)?.container_id ?? "";
  expect(containerId).toMatch(/^[0-9a-f]{64}$/);
  await expect(
    page.getByRole("paragraph").filter({
      hasText: `The command printed smoke-ok ${containerId.slice(0, 12)} /home/eika.`,
    }),
  ).toBeVisible({ timeout: 60_000 });
});
