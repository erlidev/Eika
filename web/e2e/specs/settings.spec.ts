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

    test("endpoint: what the probe found and what only the user can say", async ({
      open,
      expectShot,
    }) => {
      const eika = await open({ scenario: "providers", theme });
      await eika.click("Settings");
      await eika.click("Endpoint settings of Bench");
      await expect(eika.page.getByRole("combobox", { name: "KV cache" })).toHaveText("Not known");
      await expectShot(eika, `endpoint-bench-${theme}`, `role=dialog[name="Endpoint of Bench"]`);
    });

    test("general: a problem under the sandbox image", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", theme });
      await eika.click("Settings");
      await eika.click("General");
      await eika.fill("Sandbox image", "my image");
      // The classes above the field fill the dialog, so the problem is scrolled to.
      await eika.page.getByText(/An image reference has no spaces/).scrollIntoViewIfNeeded();
      await expect(eika.page.getByText(/An image reference has no spaces/)).toBeVisible();
      await expectShot(eika, `general-invalid-${theme}`, "role=dialog");
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

test("endpoint settings: what the user sets survives the next probe", async ({
  open,
  expectAria,
}) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Endpoint settings of Bench");
  const dialog = eika.page.getByRole("dialog", { name: /Endpoint of Bench/ });
  // The probe cannot tell whether a 2-slot llama.cpp cache is shared, and
  // says so; the user knows it is (--kv-unified).
  await expect(dialog.getByText(/does not say whether its cache is shared/)).toBeVisible();
  await expectAria(eika, "endpoint-bench-unknown", `role=dialog[name="Endpoint of Bench"]`);
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

test("endpoint settings: values the harness would refuse", async ({ open }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Endpoint settings of Bench");
  const dialog = eika.page.getByRole("dialog", { name: /Endpoint of Bench/ });
  const save = dialog.getByRole("button", { name: "Save" });
  await eika.fill("Slots", "0");
  await expect(dialog.getByLabel("Slots")).toHaveAccessibleDescription(/whole number of slots/);
  await expect(save).toBeDisabled();
  await eika.fill("Slots", "4");
  await expect(save).toBeEnabled();
});

test("edit provider: a new base URL clears the stored key", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Edit OpenAI");
  await eika.fill("Base URL", "https://api.openai.com/v2");
  await expect(eika.page.getByText(/saving clears the stored key ending in a1b2/)).toBeVisible();
  await expectAria(eika, "provider-url-changed", "role=dialog");
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

test("add a provider", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Add provider");
  await expectAria(eika, "provider-new", "role=dialog");
});

test("add models: duplicates are explained where they are", async ({ open, expectAria }) => {
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
  await expectAria(eika, "model-picker-duplicate", "role=dialog");
});

test("edit model: a name another model has", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Edit gpt-5-mini");
  await eika.fill("Name in Eika", "gpt-5");
  await expect(eika.page.getByText(/Another model is called “gpt-5”/)).toBeVisible();
  await expect(eika.page.getByRole("button", { name: "Save" })).toBeDisabled();
  await expectAria(eika, "model-dialog-duplicate", "role=dialog");
});

test("general", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("General");
  await expectAria(eika, "general", "role=dialog");
});

test("general: model classes show what they fall back to and which share a pool", async ({
  open,
  expectAria,
}) => {
  const eika = await open({ scenario: "classes" });
  await eika.click("Settings");
  await eika.click("General");
  await expect(eika.page.getByRole("combobox", { name: "Fast" })).toHaveText("Not set");
  await expect(eika.page.getByText("Uses bench-chat, the main model.")).toBeVisible();
  // Main and fast are served from the bench's pool, so they contend for its slots.
  await expect(eika.page.getByText("Shares the Bench pool with fast.")).toBeVisible();
  await expect(eika.page.getByText("Shares the Bench pool with main.")).toBeVisible();
  await expectAria(eika, "general-classes", "role=dialog");
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
  // The default model is a conversation's model too.
  expect(await options("Default model")).toEqual(["gpt-5", "gpt-5-mini", "bench-chat"]);
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

test("models: a decision model is marked, has no test and is never the default", async ({
  open,
}) => {
  const eika = await open({ scenario: "classes" });
  await eika.click("Settings");
  const row = eika.page.locator("li", { has: eika.page.getByText("d3-flash", { exact: true }) });
  await expect(row.getByText("Decision", { exact: true })).toBeVisible();
  await expect(row.getByRole("button", { name: "Test" })).toHaveCount(0);
  await expect(row.getByRole("button", { name: "Make default" })).toHaveCount(0);
  await eika.click("Edit d3-flash");
  const dialog = eika.page.getByRole("dialog", { name: "Edit d3-flash" });
  await expect(dialog.getByText(/answers typed questions, not chat/)).toBeVisible();
  await expect(dialog.getByLabel("Accepts images")).toHaveCount(0);
});

test("search: providers, keys, and quotas", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Search");
  await expectShot(eika, "search-light", "role=dialog");
});

test("search: quotas that cannot be saved", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Search");
  // A number field drops letters as they are typed, so "abc" leaves it
  // empty, which is unlimited and no problem; "1e" is text it keeps.
  await eika.fill("exa per day", "abc");
  await expect(eika.page.getByLabel("exa per day")).toHaveValue("");
  await eika.fill("brave per day", "1e");
  await eika.fill("tavily per month", "-5");
  await eika.fill("marginalia per day", "2.5");
  await eika.scroll("Save quotas");
  await expect(eika.page.getByRole("button", { name: "Save quotas" })).toBeDisabled();
  await expect(eika.page.getByText("Fix the quotas marked above to save.")).toBeVisible();
  await expectAria(eika, "search-quotas-invalid", "role=dialog");
});

test("search: try a search", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Search");
  await eika.fill("Query", "tokio runtime");
  await eika.click('role=dialog >> role=button[name="Search"]');
  await expect(eika.page.getByText(/Answered by searxng/)).toBeVisible();
  await expectAria(eika, "search-tried", "role=dialog");
});

test("compaction", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Compaction");
  await expectAria(eika, "compaction", "role=dialog");
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

test("account", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Account");
  await expectAria(eika, "account", "role=dialog");
});

test("appearance", async ({ open, expectAria }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Appearance");
  await expectAria(eika, "appearance", "role=dialog");
});

test("the dialog keeps one height on every tab", async ({ open }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  const heights: number[] = [];
  for (const tab of ["Models", "General", "Compaction", "Search", "Account", "Appearance"]) {
    await eika.click(`role=tab[name="${tab}"]`);
    const box = await eika.page.getByRole("dialog").boundingBox();
    heights.push(Math.round(box?.height ?? 0));
  }
  expect(new Set(heights).size, `the dialog resized between tabs: ${heights.join(", ")}`).toBe(1);
});

test.describe("on a phone", () => {
  test("models: rows wrap instead of cutting names", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "providers", viewport: phone });
    await eika.click("Settings");
    await expectShot(eika, "models-phone");
  });

  test("general: the classes and the pools they share", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "classes", viewport: phone });
    await eika.click("Settings");
    await eika.click("General");
    await eika.page.getByText("Model classes", { exact: true }).scrollIntoViewIfNeeded();
    await expectShot(eika, "classes-phone");
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

  test("endpoint settings fit the screen", async ({ open }) => {
    const eika = await open({ scenario: "providers", viewport: phone });
    await eika.click("Settings");
    await eika.click("Endpoint settings of Bench");
    const dialog = eika.page.getByRole("dialog", { name: /Endpoint of Bench/ });
    await expect(dialog.getByRole("button", { name: "Save" })).toBeVisible();
    const overflow = await dialog.evaluate((el) => el.scrollWidth - el.clientWidth);
    expect(overflow, "the endpoint settings are wider than the dialog").toBeLessThanOrEqual(0);
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
