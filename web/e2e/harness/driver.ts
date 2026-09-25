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
  /**
   * contractBreaks are requests the harness would refuse and answers or
   * events it would never send, by docs/api/contract.json.
   */
  contractBreaks: string[];
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
  private readonly problems: Pick<Problems, "consoleErrors" | "pageErrors"> = {
    consoleErrors: [],
    pageErrors: [],
  };

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
    // The fake clock's `performance` records no entries, and Monaco reads
    // back the typing-latency measure it just made; answer it with a zero.
    await page.addInitScript(() => {
      const entries = performance.getEntriesByName.bind(performance);
      performance.getEntriesByName = (name: string, type?: string) => {
        const found = entries(name, type);
        return found.length > 0 ? found : ([{ name, duration: 0 }] as PerformanceEntryList);
      };
    });
    // A target that is not there fails in seconds, not Playwright's 30.
    page.setDefaultTimeout(10_000);
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
      await this.inPage(async () => {
        await document.fonts.ready;
        await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
      });
    }
    // Dialog and popover enter and exit animations run for up to 200ms; wait
    // for those that end, not for a spinner that never does.
    await this.inPage(async () => {
      const finite = document
        .getAnimations()
        .filter((a) => a.effect?.getComputedTiming().endTime !== Infinity);
      await Promise.all(finite.map((a) => a.finished.catch(() => undefined)));
    });
    await this.mock.idle();
  }

  /**
   * inPage runs a script in the page, and again in the next page when a
   * step navigated away under it, as signing in to an MCP server does: the
   * browser leaves for the authorization server and comes back to the app.
   */
  private async inPage(script: () => Promise<void>): Promise<void> {
    for (let attempt = 0; ; attempt++) {
      try {
        await this.page.evaluate(script);
        return;
      } catch (err) {
        if (attempt >= 3 || !String(err).includes("Execution context was destroyed")) throw err;
        await this.page.waitForLoadState("load");
      }
    }
  }

  /**
   * locate resolves a target without waiting. A Playwright selector
   * (`role=…`, `text=…`, `css=…`, `#id`, `.class`, `[attr]`) is used as is;
   * `label=`, `placeholder=`, and `testid=` use the matching getBy method;
   * anything else is the visible name of a button, link, tab, field, or text.
   * Actions use find, which tries those kinds in order; locate merges them,
   * so it suits a target with one match, such as a screenshot's subject.
   */
  locate(target: string): Locator {
    const explicit = this.explicit(target);
    if (explicit) return explicit;
    const [first, ...rest] = this.candidates(target, this.page.locator(":root"));
    let found = first ?? this.page.getByText(target, { exact: true });
    for (const next of rest) found = found.or(next);
    return found.filter({ visible: true }).first();
  }

  /**
   * find resolves a target the way a person reads the page: a control named
   * so wins over a label, a label over a placeholder, and those over plain
   * text; and while a modal dialog is open, labels and text behind it do not
   * count. It waits up to timeoutMs for any of them to appear.
   */
  async find(target: string, timeoutMs = 10_000): Promise<Locator> {
    const explicit = this.explicit(target);
    if (explicit) return explicit;
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const modal = this.page
        .locator('[role="dialog"][aria-modal="true"], [role="alertdialog"]')
        .filter({ visible: true })
        .last();
      const scope = (await modal.count()) > 0 ? modal : this.page.locator(":root");
      for (const candidate of this.candidates(target, scope)) {
        const visible = candidate.filter({ visible: true });
        if ((await visible.count()) > 0) return visible.first();
      }
      if (Date.now() > deadline) return this.locate(target);
      await this.page.waitForTimeout(100);
    }
  }

  /** explicit is the locator for a selector target, or undefined for a bare name. */
  private explicit(target: string): Locator | undefined {
    const page = this.page;
    const [prefix, rest] = splitPrefix(target);
    if (prefix === "label") return page.getByLabel(rest, { exact: true }).first();
    if (prefix === "placeholder") return page.getByPlaceholder(rest).first();
    if (prefix === "testid") return page.getByTestId(rest).first();
    if (selectorPrefixes.some((p) => target.startsWith(p))) return page.locator(target).first();
    return undefined;
  }

  /**
   * candidates are a bare name's matches, most specific first. Roles come
   * from the accessibility tree, which already leaves out what a modal
   * hides; labels, placeholders, and text are looked up within scope.
   */
  private candidates(target: string, scope: Locator): Locator[] {
    let roles: Locator = this.page.getByRole(clickableRoles[0], { name: target, exact: true });
    for (const role of clickableRoles.slice(1)) {
      roles = roles.or(this.page.getByRole(role, { name: target, exact: true }));
    }
    return [
      roles,
      scope.getByLabel(target, { exact: true }),
      scope.getByPlaceholder(target, { exact: true }),
      scope.getByText(target, { exact: true }),
    ];
  }

  /**
   * click scrolls the target into view and lets the page settle before it
   * clicks. Scrolling can raise an overlay such as "Jump to latest"; a click
   * that met it mid-render would retry at another scroll position, so the
   * screenshot after it would depend on timing.
   */
  async click(target: string): Promise<void> {
    const found = await this.find(target);
    await found.scrollIntoViewIfNeeded();
    await this.settle();
    await found.click();
    await this.settle();
  }

  /**
   * fill replaces a field's text. A number field refuses text through
   * Playwright's fill, so there the keys are typed one by one, as a person
   * would, and the browser keeps what it accepts.
   */
  async fill(target: string, value: string): Promise<void> {
    const field = await this.find(target);
    const isNumber = await field.evaluate(
      (el) => el instanceof HTMLInputElement && el.type === "number",
    );
    if (isNumber) {
      await field.fill("");
      await field.pressSequentially(value);
    } else {
      await field.fill(value);
    }
    await this.settle();
  }

  /** select picks an option of a native select or a Radix one by its text. */
  async select(target: string, option: string): Promise<void> {
    const field = await this.find(target);
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
    await (await this.find(target)).hover();
    await this.settle();
  }

  /** scroll brings a target into view, even a disabled one that cannot be hovered. */
  async scroll(target: string): Promise<void> {
    await (await this.find(target)).scrollIntoViewIfNeeded();
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
    await (await this.find(target, timeoutMs)).waitFor({ state: "visible", timeout: timeoutMs });
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
      await (await this.find(options.target)).screenshot(common);
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
      contractBreaks: [...this.mock.contractBreaks],
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
