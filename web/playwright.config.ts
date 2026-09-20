import { defineConfig, devices } from "@playwright/test";

/**
 * Visual tests: the UI against the mock harness in e2e/harness, compared with
 * the baselines in e2e/__screenshots__. `npm run visual:update` rewrites them.
 * See e2e/README.md.
 */

/**
 * port is the Vite server the specs start. Not 4173, which `vite preview`
 * uses: a preview of an old build there would be tested instead. Set
 * EIKA_VISUAL_PORT to move it.
 */
const port = Number(process.env.EIKA_VISUAL_PORT ?? "4319");

export default defineConfig({
  testDir: "e2e/specs",
  outputDir: "e2e/test-results",
  snapshotPathTemplate: "e2e/__screenshots__/{testFileName}/{arg}{ext}",
  fullyParallel: true,
  // One browser at a time by default: each worker is a Chromium, and several
  // at once can starve a laptop. `--workers=4` runs faster where memory allows.
  workers: 1,
  forbidOnly: !!process.env.CI,
  reporter: [["list"], ["html", { outputFolder: "e2e/report", open: "never" }]],
  expect: {
    // Rendering is deterministic on one machine, so a few stray pixels are
    // the most allowed: a ratio would let a changed word through on a full page.
    toHaveScreenshot: { maxDiffPixels: 10, animations: "disabled", caret: "hide" },
  },
  use: {
    ...devices["Desktop Chrome"],
    baseURL: `http://127.0.0.1:${String(port)}`,
    trace: "retain-on-failure",
  },
  webServer: {
    command: `npx vite --port ${String(port)} --strictPort --host 127.0.0.1`,
    url: `http://127.0.0.1:${String(port)}`,
    // A server already on the port is not ours to trust: fail instead.
    reuseExistingServer: false,
  },
});
