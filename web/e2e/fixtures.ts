/**
 * The Playwright fixture for visual specs. `open` starts the page on a
 * scenario through the mock harness; `expectShot` compares the page, or one
 * element, with its committed baseline.
 */

import { expect, test as base } from "@playwright/test";

import { EikaDriver } from "./harness/driver.ts";
import type { OpenOptions } from "./harness/driver.ts";

type Fixtures = {
  /** open loads a scenario and returns the driver for it. */
  open: (options: OpenOptions) => Promise<EikaDriver>;
  /** expectShot matches a baseline named `name`, of the page or of `target`. */
  expectShot: (eika: EikaDriver, name: string, target?: string) => Promise<void>;
};

export const test = base.extend<Fixtures>({
  // Playwright names the second argument `use`; it is not a React hook.
  open: async ({ context, page }, provide) => {
    await provide((options) => EikaDriver.attach(context, page, options));
  },
  // eslint-disable-next-line no-empty-pattern -- Playwright requires a destructuring pattern.
  expectShot: async ({}, provide) => {
    await provide(async (eika, name, target) => {
      await eika.settle();
      const subject = target === undefined ? eika.page : eika.locate(target);
      await expect(subject).toHaveScreenshot(`${name}.png`);
      expect(eika.report()).toEqual({ consoleErrors: [], pageErrors: [], unhandledApi: [] });
    });
  },
});

export { expect };
