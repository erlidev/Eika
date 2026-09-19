# connect

Signing in, and React access to the connection the browser stores.

- `useAuthStatus.ts` asks `GET /api/auth/status` whether the harness has a
  sign-in password yet. It needs no token, so the app reads it first to
  choose between the setup wizard and the sign-in screen.
- `SignInScreen.tsx` exchanges the password for a session token and stores it
  with `api/connection`. Under "Advanced" it takes the deployment's API token
  instead, checked on one real route before it is stored, and another harness
  URL. A `401` anywhere clears the token and returns here.
- `LoadFailedScreen.tsx` is what a signed-in browser gets when its first
  load of the settings fails for any reason but a `401`: what failed and a
  Retry button. The browser is still signed in, so it shows no password form.
- `useConnection.ts` reads `api/connection` through `useSyncExternalStore`,
  so the app re-renders when a token appears or is forgotten.

The token itself lives in `api/connection`, not here: `api/` must not depend
on a feature, and the HTTP client needs the token. Choosing the first
password is the setup wizard's first step, in `features/setup`.

Test it: the visual specs in `web/e2e/specs/failures.spec.ts` and
`screens.spec.ts` cover a wrong password, an unreachable harness, and a
settings load that fails while signed in.
