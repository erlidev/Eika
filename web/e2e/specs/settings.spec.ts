/**
 * The Settings dialog, tab by tab: what each shows, and how a mistake in a
 * field is explained right under it with the save held back. Screens whose
 * colours matter are taken in both themes; screens whose layout matters also
 * on a phone.
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

    test("edit provider: a new base URL clears the stored key", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "providers", theme });
      await eika.click("Settings");
      await eika.click("Edit OpenAI");
      await eika.fill("Base URL", "https://api.openai.com/v2");
      await expect(
        eika.page.getByText(/saving clears the stored key ending in a1b2/),
      ).toBeVisible();
      await expectShot(eika, `provider-url-changed-${theme}`, "role=dialog");
    });

    test("general: problems under the sandbox image and the limits", async ({
      open,
      expectShot,
    }) => {
      const eika = await open({ scenario: "workbench", theme });
      await eika.click("Settings");
      await eika.click("General");
      await eika.fill("Sandbox image", "my image");
      await eika.fill("Levels below a session (1–8)", "0");
      await eika.fill("Children at a time (1–16)", "20");
      await expect(
        eika.page.getByText("Enter a whole number of levels from 1 to 8."),
      ).toBeVisible();
      await expect(
        eika.page.getByText("Enter a whole number of children from 1 to 16."),
      ).toBeVisible();
      await expectShot(eika, `general-invalid-${theme}`, "role=dialog");
    });

    test("search: providers, keys, and quotas", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", theme });
      await eika.click("Settings");
      await eika.click("Search");
      await expectShot(eika, `search-${theme}`, "role=dialog");
    });

    test("search: quotas that cannot be saved", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", theme });
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
      await expectShot(eika, `search-quotas-invalid-${theme}`, "role=dialog");
    });

    test("add models: duplicates are explained where they are", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "providers", theme });
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
      await expectShot(eika, `model-picker-duplicate-${theme}`, "role=dialog");
    });

    test("edit model: a name another model has", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "providers", theme });
      await eika.click("Settings");
      await eika.click("Edit gpt-5-mini");
      await eika.fill("Name in Eika", "gpt-5");
      await expect(eika.page.getByText(/Another model is called “gpt-5”/)).toBeVisible();
      await expect(eika.page.getByRole("button", { name: "Save" })).toBeDisabled();
      await expectShot(eika, `model-dialog-duplicate-${theme}`, "role=dialog");
    });

    test("account", async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", theme });
      await eika.click("Settings");
      await eika.click("Account");
      await expectShot(eika, `account-${theme}`, "role=dialog");
    });
  });
}

test("edit provider: URLs and names the harness would refuse", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Edit OpenAI");
  const save = eika.page.getByRole("button", { name: "Save" });
  const cases = [
    ["not a url", "invalid-url", /must be a full URL/],
    ["https://user:pw@api.openai.com/v1", "credentials", /credentials out of the URL/],
    ["https://api.openai.com/v1?key=1", "query", /query/],
  ] as const;
  for (const [url, name, problem] of cases) {
    await eika.fill("Base URL", url);
    await expect(eika.page.getByText(problem)).toBeVisible();
    await expect(save).toBeDisabled();
    await expectShot(eika, `provider-url-${name}`, "role=dialog");
  }
  await eika.fill("Base URL", "https://api.openai.com/v1");
  await eika.fill("Name", "OpenRouter");
  await expect(eika.page.getByText(/Another provider has this name/)).toBeVisible();
  await expect(save).toBeDisabled();
  await expectShot(eika, "provider-name-duplicate", "role=dialog");
});

test("add a provider", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "providers" });
  await eika.click("Settings");
  await eika.click("Add provider");
  await expectShot(eika, "provider-new", "role=dialog");
});

test("general", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("General");
  await expectShot(eika, "general", "role=dialog");
});

test("search: try a search", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Search");
  await eika.fill("Query", "tokio runtime");
  await eika.click('role=dialog >> role=button[name="Search"]');
  await eika.scroll("text=/Answered by searxng/");
  await expectShot(eika, "search-tried", "role=dialog");
});

test("appearance", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench" });
  await eika.click("Settings");
  await eika.click("Appearance");
  await expectShot(eika, "appearance", "role=dialog");
});

test.describe("on a phone", () => {
  test("models: rows wrap instead of cutting names", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "providers", viewport: phone });
    await eika.click("Settings");
    await expectShot(eika, "models-phone");
  });

  test("general", async ({ open, expectShot }) => {
    const eika = await open({ scenario: "workbench", viewport: phone });
    await eika.click("Settings");
    await eika.click("General");
    await expectShot(eika, "general-phone");
  });

  for (const theme of ["light", "dark"] as const) {
    test(`search: each provider on one line (${theme})`, async ({ open, expectShot }) => {
      const eika = await open({ scenario: "workbench", viewport: phone, theme });
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
      await expectShot(eika, theme === "light" ? "search-phone" : "search-phone-dark");
    });
  }

  test("the dialog keeps one height on every tab", async ({ open }) => {
    const eika = await open({ scenario: "providers" });
    await eika.click("Settings");
    const heights: number[] = [];
    for (const tab of ["Models", "General", "Search", "Account", "Appearance"]) {
      await eika.click(`role=tab[name="${tab}"]`);
      const box = await eika.page.getByRole("dialog").boundingBox();
      heights.push(Math.round(box?.height ?? 0));
    }
    expect(new Set(heights).size, `the dialog resized between tabs: ${heights.join(", ")}`).toBe(1);
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
