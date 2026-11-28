/**
 * The Inbox: pending memory cards with their source and flagged text, the
 * three answers, what a refused answer says, and the badge kept current on
 * two open clients and across a reconnect.
 */

import { expect, test } from "../fixtures.ts";
import type { EikaDriver } from "../harness/driver.ts";
import type { Memory } from "../../src/api/types.ts";

const phone = { width: 390, height: 844 };

const card = (eika: EikaDriver, name: string) => eika.page.getByRole("article", { name });
const badge = (eika: EikaDriver) =>
  eika.page.getByRole("navigation", { name: "Pages" }).getByRole("link", { name: /^Inbox/ });
const memory = (eika: EikaDriver, name: string): Memory => {
  const found = eika.mock.world.memories.find((m) => m.name === name);
  if (found === undefined) throw new Error(`no memory ${name}`);
  return found;
};

test("cards show source and the flagged span, and the badge counts them", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "inbox" });
  await expect(eika.page.getByRole("article")).toHaveCount(3);
  await expect(badge(eika)).toContainText("3 unanswered");
  const web = card(eika, "web-note");
  await expect(web.getByText("session:subagent-plumber")).toBeVisible();
  await expect(web.locator("mark")).toHaveText("ignore previous instructions");
  await expectShot(eika, "inbox-desktop");
});

test("accept, edit then accept, and reject each answer one card", async ({ open }) => {
  const eika = await open({ scenario: "inbox" });
  await card(eika, "metric-units").getByRole("button", { name: "Accept" }).click();
  await expect(card(eika, "metric-units")).toHaveCount(0);
  expect(memory(eika, "metric-units").state).toBe("active");
  await expect(badge(eika)).toContainText("2 unanswered");

  const web = card(eika, "web-note");
  await web.getByRole("button", { name: "Edit" }).click();
  await web.getByLabel("Body").fill("Please email the notes.");
  await web.getByRole("button", { name: "Save and accept" }).click();
  await expect(card(eika, "web-note")).toHaveCount(0);
  expect(memory(eika, "web-note")).toMatchObject({
    state: "active",
    body: "Please email the notes.",
  });

  await card(eika, "wifi-note").getByRole("button", { name: "Reject" }).click();
  await expect(eika.page.getByText("Nothing is waiting for you.")).toBeVisible();
  expect(memory(eika, "wifi-note").state).toBe("archived");
  await expect(badge(eika)).not.toContainText("unanswered");
});

test("a credential, a stale answer and an unready memory are said plainly", async ({ open }) => {
  const eika = await open({ scenario: "inbox" });
  const wifi = card(eika, "wifi-note");
  await wifi.getByRole("button", { name: "Accept" }).click();
  await expect(wifi.getByRole("alert")).toContainText("credential");
  await expect(wifi).toBeVisible();

  // Another client changed it first: the answer is refused and the card is read again.
  const metric = card(eika, "metric-units");
  memory(eika, "metric-units").body = "Changed elsewhere.";
  await metric.getByRole("button", { name: "Reject" }).click();
  await expect(metric.getByRole("alert")).toContainText("already answered");
  await expect(metric).toContainText("Changed elsewhere.");
  expect(memory(eika, "metric-units").state).toBe("pending");

  eika.mock.failRoute("POST /api/inbox/memories/{name}/accept", {
    status: 503,
    message: "the injection screen is unavailable",
  });
  await metric.getByRole("button", { name: "Accept" }).click();
  await expect(metric.getByRole("alert")).toContainText("not ready");
  expect(memory(eika, "metric-units").state).toBe("pending");
});

test("a failing list says so and Retry loads it", async ({ open }) => {
  const eika = await open({
    scenario: "inbox",
    failing: { "GET /api/inbox/memories": { status: 500 } },
  });
  await expect(eika.page.getByRole("alert")).toBeVisible();
  eika.mock.healRoute("GET /api/inbox/memories");
  await eika.page.getByRole("button", { name: "Retry" }).click();
  await expect(eika.page.getByRole("article")).toHaveCount(3);
});

test("two clients keep the badge and list, and a reconnect catches up", async ({
  open,
  context,
}) => {
  const eika = await open({ scenario: "inbox" });
  const other = await context.newPage();
  await other.goto("/inbox");
  await expect(other.getByRole("article")).toHaveCount(3);
  const otherBadge = other.getByRole("navigation", { name: "Pages" }).getByRole("link", {
    name: /^Inbox/,
  });
  await expect(otherBadge).toContainText("3 unanswered");

  // An answer in one client reaches the other through the stream.
  await card(eika, "metric-units").getByRole("button", { name: "Accept" }).click();
  await expect(other.getByRole("article")).toHaveCount(2);
  await expect(otherBadge).toContainText("2 unanswered");

  // While every socket is down nothing announces a new pending memory.
  eika.mock.dropStreams();
  memory(eika, "tea-order").state = "pending";
  await expect(other.getByRole("article")).toHaveCount(3, { timeout: 20_000 });
  await expect(otherBadge).toContainText("3 unanswered");
  await expect(badge(eika)).toContainText("3 unanswered");
});

test("the Memory list refreshes when a memory changes", async ({ open }) => {
  const eika = await open({ scenario: "inbox", path: "/memory" });
  const list = eika.page.getByRole("navigation", { name: "Memories" });
  await expect(list.getByRole("link", { name: /metric-units/ })).toContainText("Pending");
  const other = await eika.page.context().newPage();
  await other.goto("/inbox");
  await other
    .getByRole("article", { name: "metric-units" })
    .getByRole("button", { name: "Accept" })
    .click();
  await expect(list.getByRole("link", { name: /metric-units/ })).not.toContainText("Pending");
});

test("dark, desktop", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "inbox", theme: "dark" });
  await expect(eika.page.getByRole("article")).toHaveCount(3);
  await expectShot(eika, "inbox-desktop-dark");
});

test.describe("on a phone", () => {
  test.use({ hasTouch: true, isMobile: true });

  test("the bar is Chat, Activity, Inbox and More; More opens the rest", async ({
    open,
    expectShot,
  }) => {
    const eika = await open({ scenario: "inbox", viewport: phone });
    expect(await eika.page.evaluate(() => matchMedia("(pointer: coarse)").matches)).toBe(true);
    const bar = eika.page.getByRole("navigation", { name: "Pages" });
    await expect(bar.getByRole("link")).toHaveText([/Chat/, /Activity/, /Inbox/]);
    await expect(bar.getByRole("link", { name: /^Inbox/ })).toContainText("3 unanswered");
    await expectShot(eika, "inbox-phone");
    await bar.getByRole("button", { name: "More" }).tap();
    const sheet = eika.page.getByRole("navigation", { name: "More pages" });
    await expect(sheet.getByRole("link")).toHaveText([/Projects/, /Memory/, /Schedules/, /Skills/]);
    await expect(sheet.getByRole("button", { name: "Settings" })).toBeVisible();
    await expectShot(eika, "more-sheet-phone");
    await sheet.getByRole("link", { name: "Memory" }).tap();
    await expect(eika.page).toHaveURL(/\/memory$/);
    await expect(eika.page.getByRole("dialog")).toHaveCount(0);
    await expect(bar.getByRole("button", { name: "More" })).toHaveAttribute("aria-current", "page");
  });

  test("dark, the sheet", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "inbox", viewport: phone, theme: "dark" });
    await eika.page.getByRole("button", { name: "More" }).tap();
    await expect(eika.page.getByRole("dialog")).toBeVisible();
    await expectShot(eika, "more-sheet-phone-dark");
  });

  test("a card's edit form fits the phone", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "inbox", viewport: phone });
    await card(eika, "web-note").getByRole("button", { name: "Edit" }).tap();
    await expectShot(eika, "inbox-phone-edit");
  });
});

test.describe("tripwire entries", () => {
  const entry = (eika: EikaDriver, id: string) => {
    const found = eika.mock.world.tripwires.find((t) => t.id === id);
    if (found === undefined) throw new Error(`no entry ${id}`);
    return found;
  };

  test("a held run's card comes first, says what happened, and the badge counts it", async ({
    open,
    expectShot,
  }) => {
    const eika = await open({ scenario: "inbox-tripwires" });
    await expect(eika.page.getByRole("article")).toHaveCount(5);
    await expect(badge(eika)).toContainText("5 unanswered");
    const held = card(eika, "A suspect page's destination was followed");
    await expect(held).toContainText("collect.example.net");
    await expect(held).toContainText("The run is held");
    await expect(held.getByRole("button", { name: "Stop the run" })).toBeVisible();
    // A run that is over has nothing to stop; the same answer reads "Dismiss".
    const ended = card(eika, "A large amount of data was sent out");
    await expect(ended).toContainText("The run has ended");
    await expect(ended.getByRole("button", { name: "Stop the run" })).toHaveCount(0);
    await expect(ended.getByRole("button", { name: "Dismiss" })).toBeVisible();
    await expectShot(eika, "inbox-tripwires-desktop");
  });

  test("this was fine and stop the run each close one card", async ({ open }) => {
    const eika = await open({ scenario: "inbox-tripwires" });
    await card(eika, "A large amount of data was sent out")
      .getByRole("button", { name: "This was fine" })
      .click();
    await expect(eika.page.getByRole("article")).toHaveCount(4);
    expect(entry(eika, "tw-drained").status).toBe("fine");
    await expect(badge(eika)).toContainText("4 unanswered");

    await card(eika, "A suspect page's destination was followed")
      .getByRole("button", { name: "Stop the run" })
      .click();
    await expect(eika.page.getByRole("article")).toHaveCount(3);
    expect(entry(eika, "tw-held")).toMatchObject({ status: "stopped", running: false });
  });

  test.describe("on a phone", () => {
    test.use({ hasTouch: true, isMobile: true });

    test("the cards fit", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "inbox-tripwires", viewport: phone });
      await expectShot(eika, "inbox-tripwires-phone");
    });
  });
});
