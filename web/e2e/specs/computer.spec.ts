/**
 * The Computer settings tab: the home computer's limits and network. What
 * the tab says and offers is pinned by its accessibility tree; the rest is
 * asserted, including what the mock harness was sent.
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

test("the settings hold the home computer’s limits and network", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click('role=tab[name="Computer"]');
  await expectAria(eika, "computer-settings", "role=dialog");
  await eika.fill("Memory", "2048");
  await eika.click("Allowlist");
  await eika.click("Remove gitlab.com from the allowlist");
  await eika.click("Save");
  await expect(eika.page.getByText("Saved. The home computer uses these.")).toBeVisible();
  const settings = eika.mock.world.settings.settings;
  expect(settings.sandbox_limits).toEqual({ cpus: 0, memory_mb: 2048, pids: 4096 });
  expect((settings.sandbox_egress as { mode: string; allow: string[] }).allow).not.toContain(
    "gitlab.com",
  );
});

test("limits are checked against the host before they save", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click('role=tab[name="Computer"]');
  await eika.fill("CPU", "12");
  await expect(eika.page.getByLabel("CPU", { exact: true })).toHaveAccessibleDescription(
    /from 0.01 to 8/,
  );
  await expect(eika.page.getByRole("button", { name: "Save" })).toBeDisabled();
});

test("a harness outside compose offers open egress only", async ({ open }) => {
  const eika = await open({
    scenario: world("workbench", (w) => {
      w.system.sandbox.egress_control = false;
    }),
  });
  await eika.click("Settings");
  await eika.click('role=tab[name="Computer"]');
  await expect(eika.page.getByRole("radio", { name: "Allowlist" })).toBeDisabled();
  await expect(eika.page.getByRole("radio", { name: "None" })).toBeDisabled();
  await expect(eika.page.getByText(/runs outside the compose stack/)).toBeVisible();
});
