/**
 * The Computer settings tab: the home computer's limits and network. What
 * the tab says and offers is pinned by its accessibility tree; the rest is
 * asserted, including what the mock harness was sent.
 */

import { expect, test } from "../fixtures.ts";

test("the settings hold the home computer’s limits and network", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click('role=tab[name="Computer"]');
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
