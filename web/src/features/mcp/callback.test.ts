import { describe, expect, it } from "vitest";

import { callbackFromQuery } from "@/features/mcp/callback";

describe("callbackFromQuery", () => {
  it("reads the code, the state, and the issuer", () => {
    expect(callbackFromQuery("?code=abc&state=s1&iss=https%3A%2F%2Fauth.example.com")).toEqual({
      state: "s1",
      code: "abc",
      iss: "https://auth.example.com",
    });
  });

  it("tells an empty issuer from none", () => {
    expect(callbackFromQuery("?code=abc&state=s1&iss=")).toHaveProperty("iss", "");
    expect(callbackFromQuery("?code=abc&state=s1")).not.toHaveProperty("iss");
  });

  it("carries a refusal", () => {
    expect(
      callbackFromQuery("?state=s1&error=access_denied&error_description=The+user+said+no"),
    ).toEqual({
      state: "s1",
      code: "",
      error: "access_denied",
      error_description: "The user said no",
    });
  });

  it("has nothing to finish without a state", () => {
    expect(callbackFromQuery("?code=abc")).toBeNull();
    expect(callbackFromQuery("")).toBeNull();
  });
});
