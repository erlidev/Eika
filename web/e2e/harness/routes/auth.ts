/** The health check and sign-in, as internal/server/auth.go serves them. */

import { fail, ok, str } from "./context.ts";
import type { RouteContext } from "./context.ts";

/** mockToken is the bearer token the mock harness issues and accepts. */
export const mockToken = "mock-token";

/** wrongPassword is the one password sign-in refuses, to show the error. */
const wrongPassword = "wrong";

export function authRoutes(ctx: RouteContext): void {
  const { w, on } = ctx;
  on("GET", "/api/healthz", () => ok({ status: "ok" }), false);

  on("GET", "/api/auth/status", () => ok({ password_set: w.passwordSet }), false);
  on(
    "POST",
    "/api/auth/setup",
    ({ body }) => {
      if (w.passwordSet) return fail(409, "conflict", "a password is already set");
      if (str(body.password).length < 8) {
        return fail(400, "invalid_request", "password must be at least 8 characters");
      }
      w.passwordSet = true;
      return ok({ token: mockToken, expires_at: "2027-01-01T00:00:00Z" }, 201);
    },
    false,
  );
  on(
    "POST",
    "/api/auth/login",
    ({ body }) =>
      str(body.password) === wrongPassword
        ? fail(401, "unauthorized", "wrong password")
        : ok({ token: mockToken, expires_at: "2027-01-01T00:00:00Z" }),
    false,
  );
  on("POST", "/api/auth/logout", () => ({ status: 204 }));
  on("PUT", "/api/auth/password", ({ body }) =>
    str(body.current_password) === wrongPassword
      ? fail(400, "invalid_request", "current password is wrong")
      : ok({ token: mockToken, expires_at: "2027-01-01T00:00:00Z" }),
  );
}
