/**
 * The session's status bar and the model dialog, checked by what they say
 * and offer.
 */

import { expect, test } from "../fixtures.ts";

test("run status bar: the live connection is labelled", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  const live = eika.page.getByRole("status", { name: "Live updates: Live" });
  await expect(live).toBeVisible();
  await expect(live).toHaveAttribute("title", "Live updates on");
});

test("a model names the field that turns its thinking off", async ({ open }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Edit gpt-5-mini");
  const field = eika.page.getByLabel("Turn thinking off with");
  await expect(field).toHaveText("reasoning_effort");
  await field.click();
  await eika.page.getByRole("option", { name: "chat_template_kwargs" }).click();
  await expect(eika.page.getByText(/Taken by vLLM, SGLang/)).toBeVisible();
  await eika.click("Save");
  const patch = eika.mock.requests.find((r) => r.method === "PATCH");
  expect(patch?.body).toMatchObject({ thinking_switch: "chat_template_kwargs" });
});

test("the context meter states what the endpoint measured", async ({ open }) => {
  const eika = await open({ scenario: "agent-reasoning" });
  await eika.send("How should the jitter work?");
  const meter = eika.page.getByTitle(/^Context:/);
  await expect(meter).toContainText("/400k");
  // No decode rate: the endpoint reports its token counts when a response
  // ends, so a tok/s during a turn could only be a guess.
  await expect(eika.page.getByText(/tok\/s/)).toHaveCount(0);
});
