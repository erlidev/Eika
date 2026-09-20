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

test("web search, fetch, and the other tool cards", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "agent-web" });
  await eika.send("Check the cap");
  await eika.click("role=button[name^=web_search]");
  await eika.click("role=button[name^=web_fetch]");
  await expect(
    eika.page.getByRole("link", { name: "Exponential Backoff And Jitter" }),
  ).toBeVisible();
  await expectShot(eika, "agent-web-search", "role=button[name^=web_search] >> xpath=..");
  await expectShot(eika, "agent-web-fetch", "role=button[name^=web_fetch] >> xpath=..");
  // The rest of the cards, and a tool with no renderer of its own opened.
  await eika.click("role=button[name^=spawn_agent]");
  await eika.scroll("role=button[name^=spawn_agent]");
  await expectShot(eika, "agent-web-cards", "role=main");
});

test("the session tree indents only a branch", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "agent-web" });
  await eika.send("Check the cap");
  await expectShot(eika, "session-tree-branch", "role=tree");
});

test("reasoning streams beside the answer", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "agent-reasoning" });
  await eika.send("How should the jitter work?");
  // The turn's own block is the last one: the stored conversation above it
  // has reasoning of its own. Both are one line until they are opened, which
  // is what the default preference asks for.
  const thought = eika.page.getByRole("button", { name: /^Thought/ }).last();
  await expect(thought).toBeVisible();
  await expect(thought).toContainText("The cap is 30s");
  await expectShot(eika, "agent-reasoning-compact");
  await thought.click();
  await expect(eika.page.getByText(/Full jitter over the capped delay/).last()).toBeVisible();
  await expectShot(eika, "agent-reasoning-open");
});
