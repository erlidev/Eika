/**
 * Rewinding a session: taking the conversation back to a message the user
 * sent and putting it in the composer to be edited.
 */

import { expect, test } from "../fixtures.ts";

test("rewinding a message takes the conversation back and puts it in the box", async ({ open }) => {
  const eika = await open({ scenario: "agent-tools" });
  // The transcript's own rows: the same words are also in the live region
  // that announces a finished turn.
  const answers = eika.page.getByRole("region", { name: "Session" }).getByRole("article");
  await eika.send("Run the tests");
  await expect(answers.filter({ hasText: "Tests pass." })).toBeVisible();

  // The second message the user sent: the first is the stored conversation's.
  await eika.page
    .getByRole("button", { name: "Rewind the conversation to this message and edit it" })
    .nth(1)
    .click();

  // The turns that followed are off the conversation and the message is back
  // in the composer, ready to be edited and asked again.
  await expect(answers.filter({ hasText: "Tests pass." })).toHaveCount(0);
  await expect(eika.page.getByLabel("Message", { exact: true })).toHaveValue("Run the tests");
});
