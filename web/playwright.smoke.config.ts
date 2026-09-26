import { defineConfig, devices } from "@playwright/test";

/**
 * The compose smoke test: e2e/smoke against a running stack, not the mock
 * harness. `make smoke` starts the stack, sets EIKA_SMOKE_URL and
 * EIKA_SMOKE_TOKEN, and takes the stack down after.
 */
export default defineConfig({
  testDir: "e2e/smoke",
  outputDir: "e2e/test-results/smoke",
  workers: 1,
  timeout: 300_000,
  reporter: [["list"]],
  use: {
    ...devices["Desktop Chrome"],
    baseURL: process.env.EIKA_SMOKE_URL ?? "http://127.0.0.1:18080",
    actionTimeout: 20_000,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
});
