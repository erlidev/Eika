/**
 * The context meter in the session's status bar: what the endpoint measured
 * of the context window, what it served from its cache, and the next request,
 * with how each is known. The Context panel and inspector that explain it are
 * not on a page until phase 3 gives them one (PLAN.md, P3-11).
 */

import { expect, test } from "../fixtures.ts";

test("an endpoint that counts nothing: the status bar says the next request is estimated", async ({
  open,
}) => {
  const eika = await open({ scenario: "context-estimated" });
  await expect(eika.page.getByText(/next ~\d+ estimated/)).toBeVisible();
});

test("the status bar's meter: the cache, the next request and how it is known", async ({
  open,
}) => {
  const eika = await open({ scenario: "profiles" });
  // Nothing has been measured in this tab yet, so the fill and the cache are
  // n/a and the prediction stands alone, with its method.
  const bar = eika.page.getByText(/next \d+ exact/);
  await expect(bar).toBeVisible();
  await expect(eika.page.getByText("cached n/a")).toBeVisible();
  await eika.send("Now add jitter");
  // After a turn the endpoint's own figures arrive: the fill against the
  // effective window, and what it served from its cache.
  await expect(eika.page.getByText(/cached 1\.8k/)).toBeVisible();
  await expect(eika.page.getByText(/next \d+ exact/)).toBeVisible();
});
