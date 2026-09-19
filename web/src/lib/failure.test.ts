import { describe, expect, it } from "vitest";

import { ApiError, unreachableMessage } from "@/api/client";
import { failureText } from "@/lib/failure";

describe("failureText", () => {
  it("names the action and the unreachable harness", () => {
    expect(failureText("reopen the setup", new Error(unreachableMessage))).toBe(
      "Could not reopen the setup: the harness could not be reached. Check that it is running and that this browser can reach it, then try again.",
    );
  });

  it("ends a harness's lowercase reason as a sentence", () => {
    expect(failureText("add the provider", new ApiError(409, "conflict", "name taken"))).toBe(
      "Could not add the provider: name taken.",
    );
  });

  it("keeps a capital that belongs to a name", () => {
    expect(failureText("list the models", new Error("OpenAI rejected the key."))).toBe(
      "Could not list the models: OpenAI rejected the key.",
    );
  });

  it("says so when the error has no message", () => {
    expect(failureText("save the order", "boom")).toBe(
      "Could not save the order: the harness gave no reason. Try again.",
    );
  });
});
