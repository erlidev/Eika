/**
 * EikaDriver opens the UI against a mock harness and drives it in the terms a
 * person uses: click what a button is called, fill what a field is labelled,
 * send a message. It is shared by the `shot` CLI and the Playwright specs, so
 * a flow an agent tried from the command line becomes a spec unchanged.
 */

import type { Browser, BrowserContext, Locator, Page } from "@playwright/test";

import { MockHarness } from "./mock.ts";
import type { MockOptions } from "./mock.ts";
import { scenario } from "./scenarios.ts";
import { fixedNow } from "./world.ts";
import type { World } from "./world.ts";

/** OpenOptions choose the starting state and how the page looks. */
export type OpenOptions = MockOptions & {
  /** scenario is a scenario name or a World built by hand. */
  scenario: string | World;
  /** path overrides the scenario's own starting URL. */
  path?: string;
  theme?: "light" | "dark";
  viewport?: { width: number; height: number };
};

/** Problems are what went wrong on the page besides a failed step. */
export type Problems = {
  consoleErrors: string[];
  pageErrors: string[];
  /** unhandledApi are API calls the mock had no route for. */
  unhandledApi: string[];
};

/** ShotOptions narrow a screenshot to an element or widen it to the page. */
export type ShotOptions = {
  /** target captures one element instead of the viewport. */
  target?: string;
  fullPage?: boolean;
};

/** defaultViewport is a laptop screen; every committed baseline uses it. */
export const defaultViewport = { width: 1440, height: 900 };

/**
 * selectorPrefixes mark a target as a Playwright selector. Anything else is
 * the visible name of something on the page.
 */
const selectorPrefixes = ["role=", "text=", "css=", "xpath=", "internal:", "#", ".", "[", "//"];

/** clickableRoles are searched, in order, when a target is a bare name. */
const clickableRoles = [
  "button",
  "link",
  "tab",
  "menuitem",
  "option",
  "treeitem",
  "checkbox",
  "switch",
  "radio",
  "combobox",
] as const;

export class EikaDriver {
  readonly page: Page;
  readonly mock: MockHarness;
  private readonly problems: Problems = { consoleErrors: [], pageErrors: [], unhandledApi: [] };

  private constructor(page: Page, mock: MockHarness) {
    this.page = page;
    this.mock = mock;
    page.on("console", (message) => {
      // The browser logs every 4xx the mock answers on purpose; a route the
      // mock lacks is reported as unhandledApi instead.
      if (message.type() !== "error" || message.text().startsWith("Failed to load resource")) {
        return;
      }
      this.problems.consoleErrors.push(message.text());
    });
    page.on("pageerror", (error) => {
      this.problems.pageErrors.push(error.message);
    });
  }

  /** launch opens a new context on a browser, with the viewport and theme set. */
  static async launch(
    browser: Browser,
    baseURL: string,
    options: OpenOptions,
  ): Promise<EikaDriver> {
    const context = await browser.newContext({
      baseURL,
      viewport: options.viewport ?? defaultViewport,
      deviceScaleFactor: 1,
      colorScheme: options.theme ?? "light",
      reducedMotion: "reduce",
    });
    return EikaDriver.attach(context, await context.newPage(), options);
  }

  /** attach installs the mock on an existing context and opens the scenario. */
  static async attach(
    context: BrowserContext,
    page: Page,
    options: OpenOptions,
  ): Promise<EikaDriver> {
    const named = typeof options.scenario === "string" ? scenario(options.scenario) : undefined;
    const world = named ? named.build() : (options.scenario as World);
    const mock = new MockHarness(world, options);
    await mock.install(context);
    const theme = options.theme ?? "light";
    // Once per tab, so a `theme` step that reloads keeps its choice.
    await context.addInitScript((t: string) => {
      if (sessionStorage.getItem("eika.mock.theme") !== null) return;
      sessionStorage.setItem("eika.mock.theme", "1");
      localStorage.setItem("eika.theme", t);
    }, theme);
    await page.emulateMedia({ colorScheme: theme, reducedMotion: "reduce" });
    await page.setViewportSize(options.viewport ?? defaultViewport);
    // A fixed clock keeps "5 minutes ago" the same in every screenshot while
    // timers still run, so streamed replies still arrive.
    await page.clock.setFixedTime(new Date(fixedNow));
    const driver = new EikaDriver(page, mock);
    await driver.goto(options.path ?? named?.path ?? "/");
    return driver;
  }

  /** goto opens a URL of the app and waits for it to settle. */
  async goto(path: string): Promise<void> {
    await this.page.goto(path);
    await this.settle();
  }

  /**
   * settle waits until the mock has answered everything, a scripted reply has
   * finished playing, fonts have loaded, and the page has painted twice.
   */
  async settle(): Promise<void> {
    for (let round = 0; round < 3; round++) {
      await this.mock.idle();
      await this.page.evaluate(async () => {
        await document.fonts.ready;
        await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
      });
    }
    // Dialog and popover enter animations run for up to 200ms.
    await this.page.waitForTimeout(250);
    await this.mock.idle();
  }

  /**
   * locate resolves a target. A Playwright selector (`role=…`, `text=…`,
   * `css=…`, `#id`, `.class`, `[attr]`) is used as is; `label=`,
   * `placeholder=`, and `testid=` use the matching getBy method; anything else
   * is the visible name of a button, link, tab, field, or text, tried in that
   * order, and the first visible match wins.
   */
  locate(target: string): Locator {
    const page = this.page;
    const [prefix, rest] = splitPrefix(target);
    if (prefix === "label") return page.getByLabel(rest, { exact: true }).first();
    if (prefix === "placeholder") return page.getByPlaceholder(rest).first();
    if (prefix === "testid") return page.getByTestId(rest).first();
    if (selectorPrefixes.some((p) => target.startsWith(p))) return page.locator(target).first();
    let found: Locator = page.getByRole(clickableRoles[0], { name: target, exact: true });
    for (const role of clickableRoles.slice(1)) {
      found = found.or(page.getByRole(role, { name: target, exact: true }));
    }
    found = found
      .or(page.getByLabel(target, { exact: true }))
      .or(page.getByPlaceholder(target, { exact: true }))
      .or(page.getByText(target, { exact: true }));
    return found.filter({ visible: true }).first();
  }

  async click(target: string): Promise<void> {
    await this.locate(target).click();
    await this.settle();
  }

  async fill(target: string, value: string): Promise<void> {
    await this.locate(target).fill(value);
    await this.settle();
  }

  /** select picks an option of a native select or a Radix one by its text. */
  async select(target: string, option: string): Promise<void> {
    const field = this.locate(target);
    const native = await field.evaluate((el) => el.tagName === "SELECT");
    if (native) {
      await field.selectOption({ label: option });
    } else {
      await field.click();
      await this.page.getByRole("option", { name: option, exact: true }).click();
    }
    await this.settle();
  }

  async hover(target: string): Promise<void> {
    await this.locate(target).hover();
    await this.settle();
  }

  /** press sends a key or chord, like `Enter` or `Control+K`, to the focused element. */
  async press(key: string): Promise<void> {
    await this.page.keyboard.press(key);
    await this.settle();
  }

  /** type types text into the focused element. */
  async type(text: string): Promise<void> {
    await this.page.keyboard.type(text);
    await this.settle();
  }

  /** waitFor waits for a target to be visible. */
  async waitFor(target: string, timeoutMs = 5000): Promise<void> {
    await this.locate(target).waitFor({ state: "visible", timeout: timeoutMs });
    await this.settle();
  }

  /** send types a message into the session composer and sends it. */
  async send(text: string): Promise<void> {
    const composer = this.page.locator("textarea").last();
    await composer.fill(text);
    await composer.press("Enter");
    await this.settle();
  }

  async setTheme(theme: "light" | "dark"): Promise<void> {
    await this.page.evaluate((t) => {
      localStorage.setItem("eika.theme", t);
    }, theme);
    await this.page.emulateMedia({ colorScheme: theme });
    await this.page.reload();
    await this.settle();
  }

  async setViewport(width: number, height: number): Promise<void> {
    await this.page.setViewportSize({ width, height });
    await this.settle();
  }

  /** shot saves a PNG and returns its path. */
  async shot(file: string, options: ShotOptions = {}): Promise<string> {
    await this.settle();
    const common = { path: file, animations: "disabled", caret: "hide" } as const;
    if (options.target) {
      await this.locate(options.target).screenshot(common);
    } else {
      await this.page.screenshot({ ...common, fullPage: options.fullPage === true });
    }
    return file;
  }

  /**
   * aria is the page's accessibility tree as YAML: every role and name an
   * agent can use as a target, without guessing from pixels.
   */
  async aria(): Promise<string> {
    return this.page.locator("body").ariaSnapshot();
  }

  /** report lists what went wrong on the page so far. */
  report(): Problems {
    return {
      consoleErrors: [...this.problems.consoleErrors],
      pageErrors: [...this.problems.pageErrors],
      unhandledApi: [...this.mock.unhandled],
    };
  }

  async close(): Promise<void> {
    await this.page.context().close();
  }
}

function splitPrefix(target: string): [string, string] {
  const match = /^(label|placeholder|testid)=(.*)$/s.exec(target);
  return match ? [match[1] ?? "", match[2] ?? ""] : ["", target];
}
