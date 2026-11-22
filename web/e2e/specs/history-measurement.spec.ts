import { expect, test } from "../fixtures.ts";

declare global {
  interface Window {
    eikaHistoryMeasurement?: { rows: number; commits: { duration: number; rows: number }[] };
    eikaHistoryTasks?: { observer: PerformanceObserver; entries: PerformanceEntry[] };
  }
}

test.use({ hasTouch: true, isMobile: true });

test("measure a 5,000-entry transcript", async ({ open }) => {
  test.skip(process.env.EIKA_MEASURE_HISTORY !== "1", "Run explicitly for performance measurement");
  test.setTimeout(300_000);
  const start = Date.now();
  const eika = await open({
    scenario: "history-measurement",
    viewport: { width: 390, height: 844 },
  });
  const page = eika.page;
  await expect(page.getByText(/Message 5000:/)).toBeVisible();
  const initial = Date.now() - start;
  const button = page.getByRole("button", { name: "Load older messages" });
  await page.evaluate(() => {
    window.eikaHistoryMeasurement = { rows: 0, commits: [] };
    const entries: PerformanceEntry[] = [];
    const observer = new PerformanceObserver((list) => entries.push(...list.getEntries()));
    observer.observe({ type: "longtask" });
    window.eikaHistoryTasks = { observer, entries };
  });
  const loading = Date.now();
  const pageTimes: {
    page: number;
    elapsedMs: number;
    commitMs: number;
    renderedRows: number;
    longTaskMs: number;
  }[] = [];
  for (let i = 0; i < 99; i++) {
    await page.evaluate(() => {
      const measurement = window.eikaHistoryMeasurement;
      const tasks = window.eikaHistoryTasks;
      if (!measurement || !tasks) throw new Error("History measurement is missing");
      measurement.rows = 0;
      measurement.commits.length = 0;
      tasks.observer.takeRecords();
      tasks.entries.length = 0;
    });
    const started = Date.now();
    await button.click();
    const firstMessage = String(5000 - (i + 2) * 50 + 1).padStart(4, "0");
    await page.waitForFunction((message) => {
      const first = document.querySelector("[data-transcript-row]");
      return first?.textContent.includes(`Message ${message}:`) === true;
    }, firstMessage);
    if (i < 98) await expect(button).toBeEnabled();
    const elapsedMs = Date.now() - started;
    const detail = await page.evaluate(() => {
      const measurement = window.eikaHistoryMeasurement;
      const tasks = window.eikaHistoryTasks;
      if (!measurement || !tasks) throw new Error("History measurement is missing");
      tasks.entries.push(...tasks.observer.takeRecords());
      if (measurement.commits.length === 0)
        throw new Error("React profiling did not record a commit");
      return {
        commitMs: measurement.commits.reduce((sum, commit) => sum + commit.duration, 0),
        renderedRows: measurement.commits.reduce((sum, commit) => sum + commit.rows, 0),
        longTaskMs: tasks.entries.reduce((sum, task) => sum + task.duration, 0),
      };
    });
    pageTimes.push({ page: i + 1, elapsedMs, ...detail });
  }
  const loadTime = Date.now() - loading;
  expect(await page.locator("[data-transcript-row]").count()).toBe(5000);
  const scroll = await page
    .locator('[aria-label="Session"] .overflow-y-auto')
    .first()
    .evaluate(async (el) => {
      const intervals: number[] = [];
      let last = 0;
      const start = performance.now();
      await new Promise<void>((resolve) => {
        const frame = (now: number) => {
          if (last) intervals.push(now - last);
          last = now;
          el.scrollTop = (el.scrollHeight - el.clientHeight) * Math.min((now - start) / 3000, 1);
          if (now - start < 3000) requestAnimationFrame(frame);
          else resolve();
        };
        requestAnimationFrame(frame);
      });
      return {
        frames: intervals.length,
        slow: intervals.filter((ms) => ms > 20).length,
        maxMs: Math.max(...intervals),
      };
    });
  console.log(
    JSON.stringify({
      initialMs: initial,
      loadMs: loadTime,
      pages: pageTimes,
      firstPage: pageTimes[0],
      middlePage: pageTimes[49],
      lastPage: pageTimes[98],
      ...scroll,
    }),
  );
});
