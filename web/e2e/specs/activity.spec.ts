/**
 * The Activity page: Eika's subagents by state, and a read-only live view of
 * each. The tests check what the board says and that nothing on the view
 * steers; the screenshots pin the desktop split and the phone's two screens.
 */

import type { Locator } from "@playwright/test";

import { expect, test } from "../fixtures.ts";
import type { EikaDriver } from "../harness/driver.ts";

const phone = { width: 390, height: 844 };

/** rowOf is a subagent's row on the board. */
function rowOf(eika: EikaDriver, name: string | RegExp) {
  return eika.page.getByRole("navigation", { name: "Subagents" }).getByRole("link", { name });
}

/** group is a heading of the board and the rows under it. */
function group(eika: EikaDriver, title: string) {
  return eika.page.getByRole("region", { name: new RegExp(`^${title}`) });
}

/** said is a paragraph of the transcript; the live region repeats its text, so text alone matches twice. */
function said(view: Locator, text: string) {
  return view.getByRole("paragraph").filter({ hasText: text });
}

/** changed tells every client a session changed, as the harness does on each state change. */
function changed(eika: EikaDriver, id: string): void {
  eika.mock.emit(eika.mock.event("session.updated", "global", { session_id: id }));
}

/** subagent is a subagent row of the mock's world. */
function subagent(eika: EikaDriver, id: string) {
  const row = eika.mock.world.sessions.find((s) => s.id === id);
  if (row === undefined) throw new Error(`the world has no session ${id}`);
  return row;
}

test("the board groups the subagents by state, and says why one waits", async ({ open }) => {
  const eika = await open({ scenario: "activity" });
  await expect(eika.page.getByRole("heading", { level: 1, name: "Activity" })).toBeVisible();
  await expect(
    eika.page.getByRole("navigation", { name: "Pages" }).getByRole("link", { name: "Activity" }),
  ).toHaveAttribute("aria-current", "page");
  // Waiting first, then running, idle, and failed or cancelled together.
  const headings = eika.page.getByRole("navigation", { name: "Subagents" }).getByRole("heading");
  await expect(headings).toHaveText([/^Waiting/, /^Running/, /^Idle/, /^Failed and cancelled/]);
  await expect(group(eika, "Waiting").getByRole("link")).toHaveCount(2);
  await expect(rowOf(eika, /^Waiting: permit/)).toContainText(
    "Waiting on the user's answer about which council applies",
  );
  await expect(rowOf(eika, /^Waiting: quotes/)).toContainText("Waiting on a slow bash call");
  await expect(rowOf(eika, /^Failed: tiling/)).toBeVisible();
  await expect(rowOf(eika, /^Cancelled: paint/)).toBeVisible();
});

test("a subagent opens read-only: its transcript, no composer, and steering is Eika's", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "activity" });
  await rowOf(eika, /^Waiting: permit/).click();
  await expect(eika.page).toHaveURL(/\/activity\/subagent-permit$/);
  const view = eika.page.getByRole("region", { name: "Session" });
  await expect(said(view, "The rules differ by council")).toBeVisible();
  await expect(eika.page.getByRole("heading", { level: 2, name: "permit" })).toBeVisible();
  // Nothing to type into or run: no composer, no status bar, no rewind, no abort.
  await expect(eika.page.getByLabel("Message", { exact: true })).toHaveCount(0);
  await expect(eika.page.getByRole("textbox")).toHaveCount(0);
  await expect(eika.page.getByRole("button", { name: /Abort|Send|Rewind/ })).toHaveCount(0);
  await eika.page
    .getByText("Find out whether the kitchen extension needs a permit.")
    .first()
    .hover();
  await expect(eika.page.getByRole("button", { name: /Rewind/ })).toHaveCount(0);
  // The brief is folded to its first line and opens to all of it.
  await eika.click("Brief");
  await expect(eika.page.getByText("Check the council's rules.")).toBeVisible();
  await expectShot(eika, "activity-desktop-light");
});

test("desktop, dark", async ({ open, expectShot }) => {
  const eika = await open({
    scenario: "activity",
    theme: "dark",
    path: "/activity/subagent-seedlings",
  });
  await expect(eika.page.getByRole("region", { name: "Report" })).toBeVisible();
  await expectShot(eika, "activity-desktop-dark");
});

test("phone: the board is a screen of its own, and a subagent another", async ({
  open,
  expectShot,
}) => {
  const eika = await open({ scenario: "activity", viewport: phone });
  await expect(eika.page.getByText("Pick a subagent to watch it work.")).toBeHidden();
  const overflow = () =>
    eika.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(await overflow()).toBe(0);
  await expectShot(eika, "activity-board-phone");

  await rowOf(eika, /^Waiting: permit/).click();
  await expect(eika.page.getByRole("navigation", { name: "Subagents" })).toBeHidden();
  await expect(eika.page.getByRole("button", { name: "Cancel" })).toBeVisible();
  expect(await overflow()).toBe(0);
  await expectShot(eika, "activity-agent-phone");

  await eika.page.getByRole("link", { name: "Activity", exact: true }).first().click();
  await expect(eika.page).toHaveURL(/\/activity$/);
  await expect(rowOf(eika, /plumber/)).toBeVisible();
});

test("the board follows a state change as the harness reports it", async ({ open }) => {
  const eika = await open({ scenario: "activity" });
  await expect(group(eika, "Running").getByRole("link")).toHaveCount(1);
  const plumber = subagent(eika, "subagent-plumber");
  plumber.state = "waiting";
  plumber.waiting_reason = "the user's answer about the budget";
  changed(eika, plumber.id);
  await expect(group(eika, "Waiting").getByRole("link")).toHaveCount(3);
  await expect(group(eika, "Running")).toHaveCount(0);
  await expect(rowOf(eika, /^Waiting: plumber/)).toContainText("Waiting on the user's answer");

  // The reason ends and its run finishes: it is idle, with a report.
  plumber.state = "idle";
  delete plumber.waiting_reason;
  plumber.result = "Two plumbers are free next week.";
  changed(eika, plumber.id);
  await expect(group(eika, "Idle").getByRole("link", { name: /plumber/ })).toContainText(
    "Two plumbers are free next week.",
  );
});

test("the view streams what the subagent does, live", async ({ open }) => {
  const eika = await open({ scenario: "activity", path: "/activity/subagent-plumber" });
  const view = eika.page.getByRole("region", { name: "Session" });
  await expect(said(view, "Checking local options.")).toBeVisible();
  const topic = "session:subagent-plumber";
  const at = { run_id: "turn-live" };
  eika.mock.emit(
    eika.mock.event("turn.start", topic, {
      ...at,
      session_id: "subagent-plumber",
      message: "",
    }),
  );
  eika.mock.emit(eika.mock.event("message.delta", topic, { ...at, text: "Two firms " }));
  eika.mock.emit(eika.mock.event("message.delta", topic, { ...at, text: "serve the area." }));
  await expect(said(view, "Two firms serve the area.")).toBeVisible();
});

test("Cancel stops a running subagent, which stays on the board as cancelled", async ({ open }) => {
  const eika = await open({ scenario: "activity", path: "/activity/subagent-plumber" });
  await eika.click("Cancel");
  const header = eika.page.getByRole("main").locator("header");
  await expect(header).toContainText("Cancelled");
  await expect(eika.page.getByRole("button", { name: "Cancel" })).toHaveCount(0);
  await expect(group(eika, "Failed and cancelled").getByRole("link")).toHaveCount(3);
  await expect(rowOf(eika, /^Cancelled: plumber/)).toBeVisible();
  expect(subagent(eika, "subagent-plumber").state).toBe("cancelled");
  expect(Object.keys(eika.mock.world.activeRuns)).not.toContain("subagent-plumber");
});

test("a reconnect brings back what the dropped stream missed", async ({ open }) => {
  const eika = await open({ scenario: "activity", path: "/activity/subagent-plumber" });
  const view = eika.page.getByRole("region", { name: "Session" });
  await expect(said(view, "Checking local options.")).toBeVisible();
  // Everything below happens while the socket is down: no event announces it.
  eika.mock.dropStreams();
  const plumber = subagent(eika, "subagent-plumber");
  plumber.state = "idle";
  plumber.result = "Found two plumbers.";
  const entries = eika.mock.world.entries[plumber.id] ?? [];
  entries.push({
    id: "ent-late",
    seq: entries.length + 1,
    kind: "assistant",
    created_at: "2026-03-14T14:59:00Z",
    message: { role: "assistant", content: "Found two plumbers." },
    ...(plumber.head_entry_id === undefined ? {} : { parent_id: plumber.head_entry_id }),
  });
  plumber.head_entry_id = "ent-late";
  Reflect.deleteProperty(eika.mock.world.activeRuns, plumber.id);
  await expect(said(view, "Found two plumbers.")).toBeVisible({ timeout: 20_000 });
  await expect(rowOf(eika, /^Idle: plumber/)).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "Cancel" })).toHaveCount(0);
});

test("opening another subagent shows none of the last one's transcript", async ({ open }) => {
  const eika = await open({ scenario: "activity", path: "/activity/subagent-permit" });
  const view = eika.page.getByRole("region", { name: "Session" });
  await expect(said(view, "The rules differ by council")).toBeVisible();
  await rowOf(eika, /^Idle: seedlings/).click();
  await expect(said(view, "Latah, Stupice and Siberian are the usual answers.")).toBeVisible();
  await expect(said(view, "The rules differ by council")).toHaveCount(0);
  // Back on Chat, the home conversation is its own.
  await eika.click("Chat");
  const chat = eika.page.getByRole("region", { name: "Session" });
  await expect(said(chat, "I will look for one.")).toBeVisible();
  await expect(said(chat, "Latah, Stupice")).toHaveCount(0);
});

test("a question or an MCP request in a subagent shows what is asked and takes no answer", async ({
  open,
}) => {
  const eika = await open({ scenario: "activity" });
  const paint = subagent(eika, "subagent-paint");
  const entries = eika.mock.world.entries[paint.id] ?? [];
  const add = (tool: string, args: unknown, callId: string) => {
    entries.push({
      id: `ent-${callId}`,
      seq: entries.length + 1,
      kind: "assistant",
      created_at: "2026-03-14T14:00:00Z",
      message: { role: "assistant", tool_calls: [{ id: callId, name: tool, arguments: args }] },
      ...(paint.head_entry_id === undefined ? {} : { parent_id: paint.head_entry_id }),
    });
    paint.head_entry_id = `ent-${callId}`;
  };
  add("ask_user", { question: "Which finish?", options: ["Matte", "Gloss"] }, "call-ask");
  add("mcp__github__create_issue", { title: "Paint" }, "call-mcp");
  eika.mock.world.elicitations.push({
    id: "eli-paint",
    session_id: paint.id,
    run_id: "run-paint",
    call_id: "call-mcp",
    server: "github",
    mode: "form",
    message: "Which labels should the issue get?",
    requested_schema: { type: "object", properties: { label: { type: "string", title: "Label" } } },
    asked_at: "2026-03-14T14:00:00Z",
  });
  await eika.goto("/activity/subagent-paint");
  // Stored arguments must show the question before any live event, as on reload.
  const storedView = eika.page.getByRole("region", { name: "Session" });
  await expect(storedView.getByText("waiting for an answer", { exact: true })).toBeVisible();
  await expect(storedView.getByRole("list", { name: "Answer options" })).toHaveText("MatteGloss");
  eika.mock.emit(
    eika.mock.event("question.asked", "session:subagent-paint", {
      session_id: paint.id,
      run_id: "run-paint",
      call_id: "call-ask",
      question_id: "q-paint",
      question: "Which finish?",
      options: ["Matte", "Gloss"],
      allow_free_text: true,
    }),
  );
  const view = eika.page.getByRole("region", { name: "Session" });
  await expect(view.getByText("Which finish?").first()).toBeVisible();
  await expect(view.getByText("waiting for an answer", { exact: true })).toBeVisible();
  await expect(
    view.getByRole("region", { name: "github asks for input" }).getByText("Waiting for an answer."),
  ).toBeVisible();
  await expect(view.getByText("Which labels should the issue get?")).toBeVisible();
  for (const control of ["Matte", "Gloss", "Send", "Decline", "Cancel", "Submit", "Accept"]) {
    await expect(view.getByRole("button", { name: control, exact: true })).toHaveCount(0);
  }
  await expect(view.getByRole("textbox")).toHaveCount(0);
});
