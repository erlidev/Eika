/** Live notifications link to their source and do not replay on reload. */
import { expect, test } from "../fixtures.ts";
import type { EikaDriver } from "../harness/driver.ts";

function report(eika: EikaDriver) {
  const child = eika.mock.world.sessions.find((session) => session.id === "subagent-plumber");
  if (!child) throw new Error("missing plumber");
  child.state = "idle";
  child.result = "Found two plumbers.";
  eika.mock.emit(
    eika.mock.event("session.updated", "global", {
      session_id: child.id,
      report_id: "report-plumber",
    }),
  );
}

function reply(eika: EikaDriver, runId: string) {
  const home = eika.mock.world.sessions.find((session) => session.kind === "home");
  if (!home) throw new Error("missing home");
  const topic = `session:${home.id}`;
  const usage = {
    input_tokens: null,
    output_tokens: null,
    total_tokens: null,
    cached_tokens: null,
    prefilled_tokens: null,
    reasoning_tokens: null,
  };
  eika.mock.emit(
    eika.mock.event("turn.start", topic, { run_id: runId, session_id: home.id, message: "" }),
  );
  eika.mock.emit(
    eika.mock.event("message.delta", topic, { run_id: runId, text: "Here is the report." }),
  );
  eika.mock.emit(
    eika.mock.event("turn.end", topic, {
      run_id: runId,
      stop_reason: "stop",
      usage,
      context: usage,
      generation_ms: 1,
      context_window: 1000,
    }),
  );
}

for (const [name, viewport] of [
  ["desktop", { width: 1280, height: 800 }],
  ["phone", { width: 390, height: 844 }],
] as const) {
  test(`${name}: report and reply toasts open their pages`, async ({ open, expectShot }) => {
    const eika = await open({ scenario: "toasts", viewport });
    await expect(eika.page.getByRole("link", { name: /Running: plumber/ })).toBeVisible();
    const focus = eika.page.getByRole("button", { name: "All", exact: true });
    await focus.focus();
    report(eika);
    const toast = eika.page.getByRole("link", { name: "plumber reported" });
    await expect(toast).toBeVisible();
    await expect(focus).toBeFocused();
    await focus.blur();
    await expectShot(eika, `toast-report-${name}`);
    await toast.click();
    await expect(eika.page).toHaveURL(/\/activity\/subagent-plumber$/);
    await expect(eika.page.getByRole("region", { name: "Report" })).toContainText(
      "Found two plumbers.",
    );
    reply(eika, "background-1");
    const response = eika.page.getByRole("link", { name: "Eika replied" });
    await expect(response).toBeVisible();
    await response.click();
    await expect(eika.page).toHaveURL(/\/$/);
  });
}

test("a report viewed before its delayed lookup finishes is not shown later", async ({ open }) => {
  const eika = await open({ scenario: "toasts" });
  let release!: () => void;
  let requested!: () => void;
  let requests = 0;
  const started = new Promise<void>((resolve) => {
    requested = resolve;
  });
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await eika.page.route("**/api/sessions/subagent-plumber", async (route) => {
    requests++;
    requested();
    await held;
    try {
      await route.fallback();
    } catch {
      // The session view can cancel its request when it closes.
    } finally {
      requests--;
    }
  });
  report(eika);
  await started;
  await eika.page
    .getByRole("link", { name: /plumber/, exact: false })
    .first()
    .click();
  await expect(eika.page).toHaveURL(/\/activity\/subagent-plumber$/);
  await expect(eika.page.getByText("Loading the subagent…")).toBeVisible();
  await eika.page.getByRole("link", { name: "Activity", exact: true }).first().click();
  await expect(eika.page).toHaveURL(/\/activity$/);
  release();
  await expect.poll(() => requests).toBe(0);
  await eika.mock.idle();
  reply(eika, "after-lookup");
  await expect(eika.page.getByRole("link", { name: "Eika replied" })).toBeVisible();
  await expect(eika.page.getByRole("link", { name: "plumber reported" })).toHaveCount(0);
});

test("a report arriving on its view stays suppressed after leaving", async ({ open }) => {
  const eika = await open({ scenario: "toasts", path: "/activity/subagent-plumber" });
  await expect(eika.page.getByRole("heading", { name: "plumber", level: 2 })).toBeVisible();
  let release!: () => void;
  let requested!: () => void;
  let requests = 0;
  const started = new Promise<void>((resolve) => {
    requested = resolve;
  });
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await eika.page.route("**/api/sessions/subagent-plumber", async (route) => {
    requests++;
    requested();
    await held;
    try {
      await route.fallback();
    } catch {
      // The session view can cancel its request when it closes.
    } finally {
      requests--;
    }
  });
  report(eika);
  await started;
  await eika.page.getByRole("link", { name: "Activity", exact: true }).first().click();
  await expect(eika.page).toHaveURL(/\/activity$/);
  release();
  await expect.poll(() => requests).toBe(0);
  await eika.mock.idle();
  reply(eika, "after-view");
  await expect(eika.page.getByRole("link", { name: "Eika replied" })).toBeVisible();
  await expect(eika.page.getByRole("link", { name: "plumber reported" })).toHaveCount(0);
});

test("focus and hover pause dismissal", async ({ open }) => {
  const eika = await open({ scenario: "toasts" });
  reply(eika, "focus-reply");
  const toast = eika.page.getByRole("link", { name: "Eika replied" });
  const dismiss = eika.page.getByRole("button", { name: "Dismiss notification" });
  await expect(toast).toBeVisible();
  await toast.focus();
  await eika.page.waitForTimeout(5200);
  await expect(toast).toBeFocused();
  await dismiss.focus();
  await eika.page.waitForTimeout(5200);
  await expect(dismiss).toBeFocused();
  await dismiss.blur();
  await toast.hover();
  await eika.page.waitForTimeout(5200);
  await expect(toast).toBeVisible();
  await eika.page.mouse.move(0, 0);
  await expect(toast).toHaveCount(0, { timeout: 7000 });
});

test("reconnect and reload do not replay notifications", async ({ open }) => {
  const eika = await open({ scenario: "toasts" });
  await expect(eika.page.getByRole("link", { name: /Running: plumber/ })).toBeVisible();
  eika.mock.dropStreams();
  const child = eika.mock.world.sessions.find((session) => session.id === "subagent-plumber");
  if (!child) throw new Error("missing plumber");
  child.state = "idle";
  child.result = "Found two plumbers.";
  await expect(eika.page.getByRole("link", { name: /Idle: plumber/ })).toBeVisible({
    timeout: 20000,
  });
  await eika.mock.idle();
  await expect(eika.page.getByRole("link", { name: "plumber reported" })).toHaveCount(0);
  reply(eika, "after-reconnect");
  const toast = eika.page.getByRole("link", { name: "Eika replied" });
  await expect(toast).toBeVisible();
  await eika.page.getByRole("button", { name: "Dismiss notification" }).click();
  reply(eika, "after-reconnect");
  await expect(toast).toHaveCount(0);
  await eika.page.reload();
  await expect(eika.page.getByRole("link", { name: /Idle: plumber/ })).toBeVisible();
  await eika.mock.idle();
  await expect(eika.page.getByRole("link", { name: /reported|Eika replied/ })).toHaveCount(0);
  reply(eika, "after-reload");
  await expect(toast).toBeVisible();
});
