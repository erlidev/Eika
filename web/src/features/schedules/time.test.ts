import { expect, it } from "vitest";

import { formatScheduleTime } from "@/features/schedules/time";

it("shows occurrences in the schedule zone, including daylight saving time", () => {
  expect(formatScheduleTime("2026-01-15T08:00:00Z", "UTC")).toMatch(/8:00.*UTC/);
  expect(formatScheduleTime("2026-01-15T13:00:00Z", "America/New_York")).toMatch(/8:00.*EST/);
  expect(formatScheduleTime("2026-07-15T12:00:00Z", "America/New_York")).toMatch(/8:00.*EDT/);
  expect(formatScheduleTime(null, "UTC")).toBe("None");
});
