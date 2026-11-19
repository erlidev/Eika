/**
 * The Settings dialog, tab by tab: what each shows, and how a mistake in a
 * field is explained right under it with the save held back. The Models tab
 * and a form with its problems shown are compared pixel for pixel in both
 * themes, and the tabs whose layout is the point on a phone; what the other
 * tabs say and offer is checked by their accessibility tree.
 */

import { expect, test } from "../fixtures.ts";

const phone = { width: 390, height: 844 };

for (const theme of ["light", "dark"] as const) {
  test.describe(`${theme} theme`, () => {
    test("models: a key, no key needed, and a missing key", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "providers", theme });
      await eika.click("Settings");
      await expect(eika.page.getByText("no key: edit the provider to add one")).toBeVisible();
      await expectShot(eika, `models-providers-${theme}`, "role=dialog");
    });
  });
}

test("models: each provider says what its endpoint is", async ({ open }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  const card = (name: string) =>
    eika.page.locator("li", { has: eika.page.getByText(name, { exact: true }) }).first();
  await expect(card("Bench")).toContainText(
    "llama.cpp b11942 · 2 slots · 131,072 window · cache not known",
  );
  await expect(card("Local vLLM")).toContainText(
    "vLLM 0.11.0 · 1 slot · 40,960 window · cache shared 134,736",
  );
  // A failed probe is said first, and the earlier findings stay.
  await expect(card("OpenRouter")).toContainText("Last probe failed · Hosted API · 8 slots");
});

test("models: a window larger than the endpoint holds is warned about, with what to do", async ({
  open,
}) => {
  const eika = await open({ scenario: "bench-window" });
  await eika.click("Settings");
  const row = (name: string) =>
    eika.page.locator("li", { has: eika.page.getByText(name, { exact: true }) }).last();
  await expect(row("qwen-long")).toContainText(
    "Its context window is set to 200,000 tokens, but the endpoint holds 131,072 for one request",
  );
  await expect(row("qwen-long")).toContainText("Set the context window to 131,072");
  // A window the endpoint holds in full has nothing to warn about, and neither
  // has a model on an endpoint that states none.
  await expect(row("qwen-short").getByRole("status")).toHaveCount(0);
  await expect(row("gpt-5-mini").getByRole("status")).toHaveCount(0);
});

test("endpoint settings: what the user sets survives the next probe", async ({ open }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Endpoint settings of Bench");
  const dialog = eika.page.getByRole("dialog", { name: /Endpoint of Bench/ });
  // The probe cannot tell whether a 2-slot llama.cpp cache is shared, and
  // says so; the user knows it is (--kv-unified).
  await expect(dialog.getByText(/does not say whether its cache is shared/)).toBeVisible();
  const save = dialog.getByRole("button", { name: "Save" });
  await expect(save).toBeDisabled();
  await eika.click("label=KV cache");
  await eika.click("role=option[name=Shared]");
  await eika.fill("Shared cache size (tokens)", "131072");
  await expect(save).toBeEnabled();
  await eika.click("Save");
  await expect(eika.page.getByText(/cache shared 131,072/)).toBeVisible();

  // The next probe finds the same endpoint and leaves the user's cache alone.
  await eika.click("Endpoint settings of Bench");
  await expect(dialog.getByText("Set by you")).toBeVisible();
  await eika.click("Probe again");
  await expect(dialog.getByText("Probed 2026-03-14 15:00 UTC.")).toBeVisible();
  await expect(dialog.getByText("Set by you")).toBeVisible();
  await expect(dialog.getByLabel("Shared cache size (tokens)")).toHaveValue("131072");

  // Handed back, the probe's own answer returns.
  await eika.click("Use the probe's");
  await expect(dialog.getByText(/does not say whether its cache is shared/)).toBeVisible();
  await expect(dialog.getByText("Set by you")).toBeHidden();
});

test("edit provider: a new base URL clears the stored key", async ({ open }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Edit OpenAI");
  await eika.fill("Base URL", "https://api.openai.com/v2");
  await expect(eika.page.getByText(/saving clears the stored key ending in a1b2/)).toBeVisible();
});

test("edit provider: URLs and names the harness would refuse", async ({ open }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Edit OpenAI");
  const save = eika.page.getByRole("button", { name: "Save" });
  const cases = [
    ["not a url", /must be a full URL/],
    ["https://user:pw@api.openai.com/v1", /credentials out of the URL/],
    ["https://api.openai.com/v1?key=1", /query/],
  ] as const;
  for (const [url, problem] of cases) {
    await eika.fill("Base URL", url);
    // The problem is the field's own description, so a screen reader reads
    // it with the field.
    await expect(eika.page.getByLabel("Base URL")).toHaveAccessibleDescription(problem);
    await expect(save).toBeDisabled();
  }
  await eika.fill("Base URL", "https://api.openai.com/v1");
  await expect(save).toBeEnabled();
  await eika.fill("Name", "OpenRouter");
  await expect(eika.page.getByLabel("Name", { exact: true })).toHaveAccessibleDescription(
    /Another provider has this name/,
  );
  await expect(save).toBeDisabled();
});

test("add models: duplicates are explained where they are", async ({ open }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click('role=button[name="Models"] >> nth=1');
  await eika.click("gpt-5");
  await eika.fill("Model identifier", "gpt-5");
  await expect(eika.page.getByText("gpt-5 is already in the list to add below.")).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "Add", exact: true })).toBeDisabled();
  await eika.fill("Model identifier", "");
  await eika.fill("label=Name in Eika", "gpt-5");
  await expect(eika.page.getByText(/A model called “gpt-5” already exists/)).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "Add 1 model" })).toBeDisabled();
});

test("general: model classes show what they fall back to and which share a pool", async ({
  open,
}) => {
  const eika = await open({ scenario: "classes" });
  await eika.click("Settings");
  await eika.click("General");
  await expect(eika.page.getByRole("combobox", { name: "Fast" })).toHaveText("Not set");
  await expect(eika.page.getByText("Uses bench-chat, the main model.")).toBeVisible();
  // Main and fast are served from the bench's pool, so they contend for its slots.
  await expect(eika.page.getByText("Shares the Bench pool with fast.")).toBeVisible();
  await expect(eika.page.getByText("Shares the Bench pool with main.")).toBeVisible();
});

test("general: a class offers only models of its kind", async ({ open }) => {
  const eika = await open({ scenario: "classes" });
  await eika.click("Settings");
  await eika.click("General");
  const options = async (name: string) => {
    await eika.click(`role=combobox[name='${name}']`);
    const names = await eika.page.getByRole("option").allTextContents();
    await eika.page.keyboard.press("Escape");
    return names;
  };
  for (const chat of ["Deep", "Main", "Fast"]) {
    expect(await options(chat), `the ${chat} class`).toEqual([
      "Not set",
      "gpt-5",
      "gpt-5-mini",
      "bench-chat",
    ]);
  }
  expect(await options("Decision")).toEqual(["Not set", "d3-flash"]);
});

test("general: titles are skipped when fast would fall back onto a hosted deep model", async ({
  open,
}) => {
  const eika = await open({ scenario: "classes" });
  await eika.click("Settings");
  await eika.click("General");
  await eika.select("role=combobox[name='Main']", "gpt-5");
  await expect(
    eika.page.getByText(/Session titles are skipped: fast falls back onto gpt-5/),
  ).toBeVisible();
  // A fast model of the user's own turns them on again, whatever deep is.
  await eika.select("role=combobox[name='Fast']", "gpt-5");
  await expect(eika.page.getByText("Session titles are made by gpt-5.")).toBeVisible();
});

test("search: try a search", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Search");
  await eika.fill("Query", "tokio runtime");
  await eika.click('role=dialog >> role=button[name="Search"]');
  await expect(eika.page.getByText(/Answered by searxng/)).toBeVisible();
});

test("compaction: budgets the harness would refuse", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Compaction");
  await eika.fill("Reserve (tokens)", "10");
  await expect(eika.page.getByText("Enter a whole number from 1024 to 1048576.")).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "Save" })).toBeDisabled();
  await eika.fill("Reserve (tokens)", "8192");
  await eika.click("Edit the built-in prompt");
  await eika.click("Save");
  await expect(eika.page.getByText("Saved. The next run compacts with these.")).toBeVisible();
  expect(eika.mock.world.settings.settings.compaction).toMatchObject({
    reserve_tokens: 8192,
    prompts: { summary: expect.stringContaining("context checkpoint") as unknown },
  });
});

test.describe("on a phone", () => {
  test("models: rows wrap instead of cutting names", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "providers", viewport: phone });
    await eika.click("Settings");
    await expectShot(eika, "models-phone");
  });

  test("search: each provider on one line", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "workbench", viewport: phone });
    await eika.click("Settings");
    await eika.click("Search");
    // The name, its state, and both arrows share one line.
    for (const name of ["searxng", "exa", "marginalia"]) {
      const label = eika.page.locator(`label[for="provider-${name}"]`);
      const up = eika.page.getByRole("button", { name: `Move ${name} up` });
      const [a, b] = [await label.boundingBox(), await up.boundingBox()];
      if (!a || !b) throw new Error(`${name} has no label or arrow on screen`);
      // Overlapping vertically: the arrows sit beside the name, not below it.
      expect(a.y < b.y + b.height && b.y < a.y + a.height, `${name} and its arrows`).toBe(true);
    }
    await expectShot(eika, "search-phone");
  });

  test("the dialog never scrolls sideways", async ({ open }) => {
    const eika = await open({ scenario: "providers", viewport: phone });
    await eika.click("Settings");
    for (const tab of ["Models", "General", "Search", "Account", "Appearance"]) {
      await eika.click(`role=tab[name="${tab}"]`);
      const overflow = await eika.page
        .getByRole("dialog")
        .evaluate((el) => el.scrollWidth - el.clientWidth);
      expect(overflow, `the ${tab} tab is wider than the dialog`).toBeLessThanOrEqual(0);
    }
  });
});

test("notifications: save, test and remove a topic", async ({ open }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Notifications");
  await eika.fill("ntfy topic URL", "https://ntfy.example/secret-topic");
  await eika.click("Save");
  await expect(
    eika.page.getByText("A topic is configured. Enter a URL to replace it."),
  ).toBeVisible();
  await eika.click("Test");
  await expect(eika.page.getByText("Test queued. Check ntfy for the notification.")).toBeVisible();
  await eika.click("Remove");
  await expect(eika.page.getByText("Enter the full topic URL.")).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "Test", exact: true })).toHaveCount(0);
});
