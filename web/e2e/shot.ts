/**
 * The command-line way to look at the UI: open a scenario, run steps, save
 * screenshots, and print a JSON report. Built for agents; see e2e/README.md.
 *
 *   npm run shot -- --scenario workbench --step "click Diff" --step "shot diff"
 *   npm run shot -- --list
 */

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { parseArgs } from "node:util";

import { chromium } from "@playwright/test";
import type { Browser } from "@playwright/test";
import { createServer } from "vite";
import type { ViteDevServer } from "vite";

import { EikaDriver } from "./harness/driver.ts";
import { parseFailure } from "./harness/mock.ts";
import type { Failure } from "./harness/mock.ts";
import { scenarios } from "./harness/scenarios.ts";
import { parseStep, runStep, verbs } from "./harness/steps.ts";
import type { Step } from "./harness/steps.ts";

const usage = `usage: npm run shot -- [options]

  --scenario, -s <name>   starting state (default: workbench); --list shows all
  --path, -p <url>        URL to open instead of the scenario's own
  --step <step>           an action, repeatable; see steps below
  --steps <file>          read steps from a file, one per line
  --theme light|dark      colour scheme (default: light)
  --viewport <w>x<h>      window size (default: 1440x900)
  --fail "<route> = <f>"  make a route fail from the start, repeatable; <f> is a
                          status (500), "502 bare", network, or hang. Example:
                          --fail "GET /api/providers = 500"
  --out, -o <dir>         where screenshots go (default: e2e/out)
  --url <base>            use a running dev server instead of starting one
  --no-final              skip the screenshot taken after the last step
  --list                  list scenarios and exit

A screenshot named "final" is taken after the last step, with the page's
accessibility tree next to it (final.aria.yml): read it to find targets.
On a failed step, "failed.png" and "failed.aria.yml" show where it stopped.

steps:
  ${Object.values(verbs).join("\n  ")}

A target is what the UI calls something: a button's text, a field's label, a
tab's name. Playwright selectors (role=…, text=…, css=…, #id) also work.`;

type Report = {
  ok: boolean;
  scenario: string;
  url?: string;
  shots: string[];
  aria?: string;
  error?: string;
  failedStep?: string;
  consoleErrors: string[];
  pageErrors: string[];
  unhandledApi: string[];
};

async function main(): Promise<number> {
  const { values } = parseArgs({
    options: {
      scenario: { type: "string", short: "s", default: "workbench" },
      path: { type: "string", short: "p" },
      step: { type: "string", multiple: true, default: [] },
      steps: { type: "string" },
      fail: { type: "string", multiple: true, default: [] },
      theme: { type: "string", default: "light" },
      viewport: { type: "string" },
      out: { type: "string", short: "o", default: "e2e/out" },
      url: { type: "string" },
      "no-final": { type: "boolean", default: false },
      list: { type: "boolean", default: false },
      help: { type: "boolean", short: "h", default: false },
    },
  });

  if (values.help) {
    console.log(usage);
    return 0;
  }
  if (values.list) {
    for (const [name, s] of Object.entries(scenarios)) {
      console.log(`${name.padEnd(18)} ${s.path.padEnd(22)} ${s.description}`);
    }
    return 0;
  }

  const theme = values.theme === "dark" ? "dark" : "light";
  let viewport: { width: number; height: number } | undefined;
  if (values.viewport) {
    const m = /^(\d+)x(\d+)$/.exec(values.viewport);
    if (!m) throw new Error("--viewport takes <width>x<height>, e.g. 390x844");
    viewport = { width: Number(m[1]), height: Number(m[2]) };
  }

  const lines = [...values.step];
  if (values.steps) lines.push(...readFileSync(values.steps, "utf8").split("\n"));
  const steps = lines.map(parseStep).filter((s): s is Step => s !== null);

  const failing: Record<string, Failure> = {};
  for (const flag of values.fail) {
    const at = flag.lastIndexOf("=");
    if (at < 0) throw new Error(`--fail takes "<route> = <failure>", got "${flag}"`);
    failing[flag.slice(0, at).trim()] = parseFailure(flag.slice(at + 1));
  }

  const out = resolve(values.out);
  mkdirSync(out, { recursive: true });
  const shotPath = (name: string) => resolve(out, `${name.replace(/\.png$/, "")}.png`);

  const report: Report = {
    ok: true,
    scenario: values.scenario,
    shots: [],
    consoleErrors: [],
    pageErrors: [],
    unhandledApi: [],
  };

  let server: ViteDevServer | undefined;
  let baseURL = values.url;
  if (!baseURL) {
    server = await createServer({
      configFile: resolve(import.meta.dirname, "../vite.config.ts"),
      root: resolve(import.meta.dirname, ".."),
      logLevel: "error",
      server: { port: 0, strictPort: false, hmr: false },
    });
    await server.listen();
    baseURL = server.resolvedUrls?.local[0];
    if (!baseURL) throw new Error("vite started without a local URL");
  }

  const browser = await chromium.launch();
  if (server) await warmUp(browser, baseURL);
  let driver: EikaDriver | undefined;
  try {
    driver = await EikaDriver.launch(browser, baseURL, {
      scenario: values.scenario,
      theme,
      failing,
      ...(values.path ? { path: values.path } : {}),
      ...(viewport ? { viewport } : {}),
    });
    const hooks = {
      shotPath,
      onShot: (file: string) => {
        report.shots.push(file);
      },
      onAria: (yaml: string) => {
        const file = resolve(out, "page.aria.yml");
        writeFileSync(file, yaml);
        report.aria = file;
      },
    };
    for (const step of steps) {
      try {
        await runStep(driver, step, hooks);
      } catch (error) {
        report.ok = false;
        report.failedStep = step.line;
        report.error = firstLine(error);
        await driver.shot(shotPath("failed"));
        report.shots.push(shotPath("failed"));
        const aria = resolve(out, "failed.aria.yml");
        writeFileSync(aria, await driver.aria());
        report.aria = aria;
        break;
      }
    }
    if (report.ok && !values["no-final"]) {
      report.shots.push(await driver.shot(shotPath("final")));
      const aria = resolve(out, "final.aria.yml");
      writeFileSync(aria, await driver.aria());
      report.aria = aria;
    }
    report.url = driver.page.url();
    Object.assign(report, driver.report());
  } catch (error) {
    report.ok = false;
    report.error = firstLine(error);
  } finally {
    await driver?.close();
    await browser.close();
    await server?.close();
  }

  console.log(JSON.stringify(report, null, 2));
  return report.ok ? 0 : 1;
}

/**
 * warmUp loads the app once and throws the page away. A cold Vite cache
 * optimises dependencies on first load and then reloads the page, which
 * would otherwise land in the middle of the first step.
 */
async function warmUp(browser: Browser, baseURL: string): Promise<void> {
  const page = await browser.newPage();
  await page.route(
    (url) => url.pathname.startsWith("/api/"),
    (route) => route.abort(),
  );
  await page.goto(baseURL);
  await page.waitForLoadState("networkidle");
  await page.close();
}

/** firstLine keeps a Playwright error readable: its call log is noise here. */
function firstLine(error: unknown): string {
  const text = error instanceof Error ? error.message : String(error);
  return text.split("\nCall log:")[0]?.trim() ?? text;
}

main().then(
  (code) => process.exit(code),
  (error: unknown) => {
    console.error(error instanceof Error ? error.message : error);
    process.exit(2);
  },
);
