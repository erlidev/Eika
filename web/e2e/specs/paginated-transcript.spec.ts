import { expect, test } from "../fixtures.ts";

test("loads older messages without moving the visible row", async ({ open }) => {
  const eika = await open({ scenario: "paginated-transcript" });
  const page = eika.page;
  await expect(page.getByText("Message 120", { exact: true })).toBeVisible();
  await expect(page.getByText("Message 070", { exact: true })).toHaveCount(0);
  const button = page.getByRole("button", { name: "Load older messages" });
  await button.scrollIntoViewIfNeeded();
  const row = page.getByText("Message 071", { exact: true });
  const top = (await row.boundingBox())?.y ?? 0;
  await button.click();
  await expect(page.getByText("Message 021", { exact: true })).toBeAttached();
  expect(Math.abs(((await row.boundingBox())?.y ?? 0) - top)).toBeLessThan(2);
  await button.click();
  await expect(page.getByText("Message 001", { exact: true })).toBeAttached();
  await expect(button).toHaveCount(0);
});
