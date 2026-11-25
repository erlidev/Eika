/**
 * The session's status bar and the model dialog, checked by what they say
 * and offer.
 */

import { expect, test } from "../fixtures.ts";

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
