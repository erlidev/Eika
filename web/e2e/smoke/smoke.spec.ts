/**
 * The compose smoke test: the real stack, from the guided setup to a run
 * whose shell command executes in a sandbox container. `make smoke` starts
 * the stack with compose.smoke.yaml, runs this, and takes the stack down.
 * The model is `model.ts`, a scripted endpoint that asks for `echo smoke-ok`
 * and then reports what it printed.
 */

import { execFileSync } from "node:child_process";
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { expect, test } from "@playwright/test";
import type { APIRequestContext } from "@playwright/test";

const token = process.env.EIKA_SMOKE_TOKEN ?? "";
const project = "smoke-demo";

/** api calls the harness with the fixed token compose.smoke.yaml sets. */
function api(request: APIRequestContext, method: string, path: string) {
  return request.fetch(path, { method, headers: { Authorization: `Bearer ${token}` } });
}

let checkout = "";

test.beforeAll(async ({ request }) => {
  // A local project: a git checkout on this machine, which the Docker daemon
  // bind-mounts into the workspace. The sandbox runs as uid 1000, which may
  // not be this user.
  checkout = mkdtempSync(join(tmpdir(), "eika-smoke-"));
  writeFileSync(join(checkout, "README.md"), "# smoke\n");
  const git = (...args: string[]) =>
    execFileSync("git", ["-c", "user.name=smoke", "-c", "user.email=smoke@eika", ...args], {
      cwd: checkout,
    });
  git("init", "-q", "-b", "main");
  git("add", ".");
  git("commit", "-q", "-m", "init");
  execFileSync("chmod", ["-R", "a+rwX", checkout]);
  chmodSync(checkout, 0o777);

  // The harness waits for postgres and the sandbox image before it listens.
  await expect
    .poll(async () => (await request.get("/healthz").catch(() => null))?.status(), {
      timeout: 180_000,
      intervals: [1000],
    })
    .toBe(200);
});

test.afterAll(async ({ request }) => {
  // Workspaces are sibling containers compose does not own, so the test
  // removes its own before the stack goes down.
  const res = await api(request, "GET", "/api/workspaces");
  if (res.ok()) {
    const { workspaces } = (await res.json()) as { workspaces: { id: string }[] };
    for (const w of workspaces) {
      await api(request, "DELETE", `/api/workspaces/${w.id}`);
    }
  }
  if (checkout) rmSync(checkout, { recursive: true, force: true });
});

test("setup, a workspace, and a run that executes in the sandbox", async ({ page }) => {
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

  // Sandbox: Docker and the sandbox image are there.
  await expect(page.getByText("The harness can start containers.")).toBeVisible();
  await expect(page.getByText("is ready.")).toBeVisible();
  await page.getByRole("button", { name: "Continue" }).click();

  // First project.
  await page.getByLabel("Name").fill(project);
  await page.getByLabel("Path on the Docker host").fill(checkout);
  await page.getByRole("button", { name: "Add and finish" }).click();

  // A workspace on it.
  const projects = page.getByRole("navigation", { name: "Projects" });
  await projects.getByText(project).hover();
  await page.getByRole("button", { name: `New workspace in ${project}` }).click();
  await page.getByRole("dialog").getByLabel("Name").fill("smoke-ws");
  await page.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("dialog")).toBeHidden();

  // A session in it, once the container runs.
  const projectRow = projects.getByRole("button", { name: new RegExp(`^${project}`) });
  if ((await projectRow.getAttribute("aria-expanded")) === "false") await projectRow.click();
  await projects.getByText("smoke-ws").hover();
  const newSession = page.getByRole("button", { name: "New session in smoke-ws" });
  await expect(newSession).toBeEnabled({ timeout: 120_000 });
  await newSession.click();

  await page.getByLabel("Message").fill("Say hello from the sandbox");
  await page.getByLabel("Message").press("Enter");

  await expect(page.getByText("The command printed smoke-ok.")).toBeVisible({ timeout: 60_000 });
});
