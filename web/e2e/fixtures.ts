/**
 * The Playwright fixture for the specs. `open` starts the page on a scenario
 * through the mock harness; `expectShot` compares the page, or one element,
 * with its committed baseline; `expectAria` compares an element's
 * accessibility tree with its committed snapshot.
 *
 * Every test also fails on what went wrong around it: a console error, a page
 * error, an API call the mock does not serve, or a request, answer, or event
 * that breaks the API contract.
 */

import { expect, test as base } from "@playwright/test";

import { EikaDriver } from "./harness/driver.ts";
import type { OpenOptions } from "./harness/driver.ts";

type Fixtures = {
  /** open loads a scenario and returns the driver for it. */
  open: (options: OpenOptions) => Promise<EikaDriver>;
  /** expectShot matches a baseline named `name`, of the page or of `target`. */
  expectShot: (eika: EikaDriver, name: string, target?: string) => Promise<void>;
  /**
   * expectAria matches the accessibility tree of `target` with the snapshot
   * named `name`: its roles, names, and text, with no pixels. It suits a
   * check of what a screen says and offers; expectShot is for how it looks.
   */
  expectAria: (eika: EikaDriver, name: string, target?: string) => Promise<void>;
};

export const test = base.extend<Fixtures>({
  // Playwright names the second argument `use`; it is not a React hook.
  open: async ({ context, page }, provide) => {
    const opened: EikaDriver[] = [];
    await provide(async (options) => {
      const driver = await EikaDriver.attach(context, page, options);
      opened.push(driver);
      return driver;
    });
    for (const driver of opened) {
      expect(driver.report(), "problems on the page or at the API").toEqual({
        consoleErrors: [],
        pageErrors: [],
        unhandledApi: [],
        contractBreaks: [],
      });
    }
  },
  // eslint-disable-next-line no-empty-pattern -- Playwright requires a destructuring pattern.
  expectShot: async ({}, provide) => {
    await provide(async (eika, name, target) => {
      await eika.settle();
      const subject = target === undefined ? eika.page : await eika.find(target);
      await expect(subject).toHaveScreenshot(`${name}.png`);
    });
  },
  // eslint-disable-next-line no-empty-pattern -- Playwright requires a destructuring pattern.
  expectAria: async ({}, provide) => {
    await provide(async (eika, name, target) => {
      await eika.settle();
      const subject = target === undefined ? eika.page.locator("body") : await eika.find(target);
      await expect(subject).toMatchAriaSnapshot({ name: `${name}.aria.yml` });
    });
  },
});

export { expect };
