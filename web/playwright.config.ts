import { defineConfig, devices } from "@playwright/test";

/**
 * Visual tests: the UI against the mock harness in e2e/harness, compared with
 * the baselines in e2e/__screenshots__. `npm run visual:update` rewrites them.
 * See e2e/README.md.
 */
export default defineConfig({
  testDir: "e2e/specs",
  outputDir: "e2e/test-results",
  snapshotPathTemplate: "e2e/__screenshots__/{testFileName}/{arg}{ext}",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  reporter: [["list"], ["html", { outputFolder: "e2e/report", open: "never" }]],
  expect: {
    toHaveScreenshot: { maxDiffPixelRatio: 0.002, animations: "disabled", caret: "hide" },
  },
  use: {
    ...devices["Desktop Chrome"],
    baseURL: "http://127.0.0.1:4173",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "npx vite --port 4173 --strictPort --host 127.0.0.1",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: !process.env.CI,
  },
});
