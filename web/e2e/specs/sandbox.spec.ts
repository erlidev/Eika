/**
 * A workspace's sandbox: the Sandbox panel's usage, refused hosts, previews,
 * and editor; the defaults in the settings' Sandbox tab; and the sandbox of
 * a new workspace. What the panel says and offers is pinned by its
 * accessibility tree; the rest is asserted, including what the mock harness
 * was sent.
 */

import { expect, test } from "../fixtures.ts";
import { scenario } from "../harness/scenarios.ts";
import type { World } from "../harness/world.ts";

/** world builds a scenario's world and changes it before the page loads. */
function world(name: string, change: (w: World) => void): World {
  const w = scenario(name).build();
  change(w);
  return w;
}

test("the panel shows the usage against the limits and what was refused", async ({
  open,
  expectAria,
}) => {
  const eika = await open({ scenario: "sandbox" });
  await eika.click("Sandbox");
  await expect(eika.page.getByText("38% of 2 cores")).toBeVisible();
  await expect(eika.page.getByText("612 MiB of 4.0 GiB")).toBeVisible();
  await expect(eika.page.getByRole("meter", { name: "Memory used" })).toHaveAttribute(
    "aria-valuenow",
    "15",
  );
  await expectAria(eika, "sandbox-panel", "role=tabpanel");
});

test("a refused host is one click from the allowlist", async ({ open }) => {
  const eika = await open({ scenario: "sandbox" });
  await eika.click("Sandbox");
  await eika.click("Allow sum.golang.org");
  await expect(
    eika.page.getByRole("button", { name: "Remove sum.golang.org from the allowlist" }),
  ).toBeVisible();
  const ws = eika.mock.world.workspaces.find((w) => w.id === "ws-retries");
  expect(ws?.sandbox.egress.allow).toContain("sum.golang.org");
});

test("limits are checked against the host and applied at once", async ({ open }) => {
  const eika = await open({ scenario: "sandbox" });
  await eika.click("Sandbox");
  const apply = eika.page.getByRole("button", { name: "Apply" });
  await expect(apply).toBeDisabled();

  await eika.fill("CPU", "12");
  await expect(eika.page.getByLabel("CPU", { exact: true })).toHaveAccessibleDescription(
    /from 0.01 to 8/,
  );
  await expect(apply).toBeDisabled();

  await eika.fill("CPU", "1.5");
  await eika.fill("Memory", "");
  await eika.click("None");
  await eika.click("Apply");
  await expect(apply).toBeDisabled();
  const ws = eika.mock.world.workspaces.find((w) => w.id === "ws-retries");
  expect(ws?.sandbox.limits).toEqual({ cpus: 1.5, memory_mb: 0, pids: 4096 });
  expect(ws?.sandbox.egress.mode).toBe("none");
  // What was saved is what the editor starts from again.
  await expect(eika.page.getByLabel("CPU", { exact: true })).toHaveValue("1.5");
});

test("a forwarded port is added in the editor and previewed from the list", async ({ open }) => {
  const eika = await open({ scenario: "sandbox" });
  await eika.click("Sandbox");
  await eika.click("Add port");
  await eika.fill("Port 2", "5173");
  await expect(eika.page.getByText("Port 5173 is listed twice.")).toBeVisible();
  await eika.fill("Port 2", "8080");
  await eika.fill("Label of port 2", "api");
  await eika.click("Apply");
  await expect(eika.page.getByRole("button", { name: "Open port 8080" })).toBeEnabled();
  const ws = eika.mock.world.workspaces.find((w) => w.id === "ws-retries");
  expect(ws?.sandbox.ports).toEqual([
    { port: 5173, label: "vite" },
    { port: 8080, label: "api" },
  ]);
});

test("a stopped workspace shows no usage and can still be changed", async ({ open }) => {
  const eika = await open({
    scenario: world("sandbox", (w) => {
      const stopped = w.workspaces.find((x) => x.id === "ws-retries");
      if (stopped !== undefined) stopped.state = "stopped";
    }),
    path: "/sessions/ses-backoff",
  });
  const ws = eika.mock.world.workspaces.find((w) => w.id === "ws-retries");
  await eika.click("Sandbox");
  await expect(
    eika.page.getByText(/The workspace is stopped\. Its usage shows while it runs/),
  ).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "Open port 5173" })).toBeDisabled();
  await eika.fill("Processes", "512");
  await eika.click("Apply");
  expect(ws?.sandbox.limits.pids).toBe(512);
});

test("a harness outside compose offers open egress only", async ({ open }) => {
  const eika = await open({
    scenario: world("workbench", (w) => {
      w.system.sandbox.egress_control = false;
    }),
    path: "/sessions/ses-backoff",
  });
  await eika.click("Sandbox");
  await expect(eika.page.getByRole("radio", { name: "Allowlist" })).toBeDisabled();
  await expect(eika.page.getByRole("radio", { name: "None" })).toBeDisabled();
  await expect(eika.page.getByText(/runs outside the compose stack/)).toBeVisible();
});

test("the settings hold the defaults a new workspace gets", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click('role=tab[name="Sandbox"]');
  await expectAria(eika, "sandbox-settings", "role=dialog");
  await eika.fill("Memory", "2048");
  await eika.click("Allowlist");
  await eika.click("Remove gitlab.com from the allowlist");
  await eika.click("Save");
  await expect(
    eika.page.getByText("Saved. The next workspace is created with these."),
  ).toBeVisible();
  const settings = eika.mock.world.settings.settings;
  expect(settings.sandbox_limits).toEqual({ cpus: 0, memory_mb: 2048, pids: 4096 });
  expect((settings.sandbox_egress as { mode: string; allow: string[] }).allow).not.toContain(
    "gitlab.com",
  );
});

test("a new workspace gets the defaults unless its own sandbox is opened", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  const create = async (name: string, own: boolean) => {
    await eika.hover("web-dashboard");
    await eika.click("New workspace in web-dashboard");
    await eika.fill("Name", name);
    if (own) {
      await eika.click("Sandbox the defaults from Settings, Sandbox");
      await eika.fill("CPU", "2");
      await eika.click("Allowlist");
      await eika.fill("Allowed hosts", "https://example.com/some/path");
      await eika.press("Enter");
      await expect(
        eika.page.getByRole("button", { name: "Remove example.com from the allowlist" }),
      ).toBeVisible();
    }
    await eika.click("Create");
    await expect(eika.page.getByRole("dialog")).toBeHidden();
    return eika.mock.world.workspaces.find((w) => w.name === name);
  };

  const plain = await create("plain", false);
  expect(plain?.sandbox.limits).toEqual({ cpus: 0, memory_mb: 0, pids: 4096 });
  expect(plain?.sandbox.egress.mode).toBe("open");

  const own = await create("confined", true);
  expect(own?.sandbox.limits.cpus).toBe(2);
  expect(own?.sandbox.egress.mode).toBe("allowlist");
  expect(own?.sandbox.egress.allow).toContain("example.com");
});
