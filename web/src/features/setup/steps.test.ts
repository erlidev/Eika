import { describe, expect, it } from "vitest";

import { firstStep, nextStep, setupSteps, stepIndex, stepInfo } from "@/features/setup/steps";

describe("firstStep", () => {
  it("starts with the password on a new harness", () => {
    expect(firstStep({ passwordSet: false, providers: 0, models: 0 })).toBe("account");
  });

  it("asks for a password even when providers exist", () => {
    // A harness first reached with the API token has models but no password.
    expect(firstStep({ passwordSet: false, providers: 1, models: 3 })).toBe("account");
  });

  it("resumes at the first thing still missing", () => {
    expect(firstStep({ passwordSet: true, providers: 0, models: 0 })).toBe("provider");
    expect(firstStep({ passwordSet: true, providers: 1, models: 0 })).toBe("models");
    expect(firstStep({ passwordSet: true, providers: 1, models: 2 })).toBe("system");
  });
});

describe("the step order", () => {
  it("walks every step once and ends", () => {
    const walked = [setupSteps[0]?.id];
    for (let step = nextStep("account"); step !== null; step = nextStep(step)) {
      walked.push(step);
    }
    expect(walked).toEqual(["account", "provider", "models", "system", "project"]);
  });

  it("numbers and describes each step", () => {
    expect(stepIndex("models")).toBe(2);
    expect(stepInfo("project").label).toBe("Project");
  });
});
