import { defineConfig, devices } from "@playwright/test";

/**
 * Visual tests: the UI against the mock harness in e2e/harness, compared with
 * the baselines in e2e/__screenshots__. `npm run visual:update` rewrites them.
 * See e2e/README.md.
 */

/**
 * port is the server the specs start. Not 4173, which `vite preview` uses by
 * default: a preview of an old build there would be tested instead. Set
 * EIKA_VISUAL_PORT to move it.
 */
const port = Number(process.env.EIKA_VISUAL_PORT ?? "4319");

/**
 * dist is the build the specs load. A fresh browser context per test would
 * fetch the dev server's hundreds of unbundled modules every time; a build
 * takes seconds once and loads a few files per test.
 */
const dist = "e2e/dist";

export default defineConfig({
  testDir: "e2e/specs",
  outputDir: "e2e/test-results",
  snapshotPathTemplate: "e2e/__screenshots__/{testFileName}/{arg}{ext}",
  fullyParallel: true,
  // Each worker is a Chromium; half the cores keeps a laptop usable while the
  // suite runs. `--workers=1` runs one browser at a time.
  workers: "50%",
  forbidOnly: !!process.env.CI,
  reporter: [["list"], ["html", { outputFolder: "e2e/report", open: "never" }]],
  expect: {
    // Rendering is deterministic on one machine, so a few stray pixels are
    // the most allowed: a ratio would let a changed word through on a full page.
    toHaveScreenshot: { maxDiffPixels: 10, animations: "disabled", caret: "hide" },
    toMatchAriaSnapshot: { pathTemplate: "e2e/__aria__/{testFileName}/{arg}{ext}" },
  },
  use: {
    ...devices["Desktop Chrome"],
    baseURL: `http://127.0.0.1:${String(port)}`,
    trace: "retain-on-failure",
  },
  webServer: {
    command: [
      `npx vite build --outDir ${dist} --emptyOutDir --logLevel error`,
      `npx vite preview --outDir ${dist} --port ${String(port)} --strictPort --host 127.0.0.1`,
    ].join(" && "),
    url: `http://127.0.0.1:${String(port)}`,
    // A server already on the port is not ours to trust: fail instead.
    reuseExistingServer: false,
  },
});
