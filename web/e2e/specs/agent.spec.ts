/**
 * The session view while an agent works: streamed tools, a question, a
 * failure, and a run in progress, each driven by a scripted mock reply. The
 * transcript cards are checked by their content and interactions. The web
 * search and fetch cards also have pixel baselines.
 */

import { expect, test } from "../fixtures.ts";

test("tool calls stream and finish", async ({ open }) => {
  const eika = await open({ scenario: "agent-tools" });
  await eika.send("Run the tests");
  await eika.click("go test ./internal/webhook/...");
  await expect(eika.page.getByText("exit code 0")).toBeVisible();
});

test("a question waits for an answer", async ({ open }) => {
  const eika = await open({ scenario: "agent-question" });
  await eika.send("Change the cap");
  await eika.click("30s");
  await expect(eika.page.getByText("Using that cap.").first()).toBeVisible();
  // Answered, the question offers its options no more.
  await expect(eika.page.getByRole("button", { name: "30s" })).toHaveCount(0);
});

test("a failed run shows its error", async ({ open }) => {
  const eika = await open({ scenario: "agent-error" });
  await eika.send("Try again");
  await expect(eika.page.getByText(/rate limit exceeded/).first()).toBeVisible();
});

test("a running agent can be aborted", async ({ open }) => {
  const eika = await open({ scenario: "agent-running" });
  await eika.send("Refactor everything");
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
  await eika.click("role=button[name^=read_note]");
  await expect(eika.page.getByRole("button", { name: /^read_note/ })).toHaveAttribute(
    "aria-expanded",
    "true",
  );
});

test("reasoning streams beside the answer", async ({ open }) => {
  const eika = await open({ scenario: "agent-reasoning" });
  await eika.send("How should the jitter work?");
  // The turn's own block is the last one: the stored conversation above it
  // has reasoning of its own. Both are one line until they are opened, which
  // is what the default preference asks for.
  const thought = eika.page.getByRole("button", { name: /^Thought/ }).last();
  await expect(thought).toBeVisible();
  await expect(thought).toContainText("The cap is 30s");
  await expect(thought).toHaveAttribute("aria-expanded", "false");
  await thought.click();
  await expect(eika.page.getByText(/Full jitter over the capped delay/).last()).toBeVisible();
});

test("an answer the endpoint cut off is kept, and says why it stopped", async ({ open }) => {
  const eika = await open({ scenario: "agent-cut-off" });
  await eika.send("Write the file");
  // The text stays on screen: throwing it away would leave the reader with a
  // turn that produced nothing and the model with a history it never wrote.
  // The same words are also in the live region, so this
  // names the paragraph in the transcript.
  await expect(
    eika.page.getByText("Writing the file now. It starts like this:").first(),
  ).toBeVisible();
  await expect(eika.page.getByText(/Cut off at the model's output limit/)).toBeVisible();
});

test("/compact in the message box compacts with a focus", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.page.getByLabel("Message", { exact: true }).fill("/compact the retry tests");
  await expect(
    eika.page.getByRole("button", { name: "Compact", exact: true }).last(),
  ).toBeVisible();
  await eika.press("Enter");
  const divider = eika.page.getByRole("button", { name: /Compacted about/ });
  await divider.click();
  await expect(eika.page.getByText("the retry tests")).toBeVisible();
  // What the compaction kept is all there is now, so a second one has nothing to do.
  await eika.click('role=button[name="Compact"]');
  await expect(
    eika.page.getByText(/Could not compact the conversation: nothing to compact/),
  ).toBeVisible();
});
