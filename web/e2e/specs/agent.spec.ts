/**
 * The session view while an agent works: streamed tools, a question, a
 * failure, and a run in progress, each driven by a scripted mock reply.
 */

import { expect, test } from "../fixtures.ts";

test("tool calls stream and finish", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "agent-tools" });
  await eika.send("Run the tests");
  await eika.click("go test ./internal/webhook/...");
  await expect(eika.page.getByText("exit code 0")).toBeVisible();
  await expectShot(eika, "agent-tools");
});

test("a question waits for an answer", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "agent-question" });
  await eika.send("Change the cap");
  await expectShot(eika, "agent-question");
  await eika.click("30s");
  await expect(eika.page.getByText("Using that cap.").first()).toBeVisible();
  await expectShot(eika, "agent-question-answered");
});

test("a failed run shows its error", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "agent-error" });
  await eika.send("Try again");
  await expect(eika.page.getByText(/rate limit exceeded/).first()).toBeVisible();
  await expectShot(eika, "agent-error");
});

test("a running agent can be aborted", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "agent-running" });
  await eika.send("Refactor everything");
  await expectShot(eika, "agent-running");
  await eika.click("Abort");
  await expect(eika.page.getByRole("button", { name: "Abort" })).toBeHidden();
});
