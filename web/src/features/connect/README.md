# connect

Signing in, and React access to the connection the browser stores.

- `useAuthStatus.ts` asks `GET /api/auth/status` whether the harness has a
  sign-in password yet. It needs no token, so the app reads it first to
  choose between the setup wizard and the sign-in screen.
- `SignInScreen.tsx` exchanges the password for a session token and stores it
  with `api/connection`. Under "Advanced" it takes the deployment's API token
  instead, checked on one real route before it is stored, and another harness
  URL. A `401` anywhere clears the token and returns here.
- `useConnection.ts` reads `api/connection` through `useSyncExternalStore`,
  so the app re-renders when a token appears or is forgotten.

The token itself lives in `api/connection`, not here: `api/` must not depend
on a feature, and the HTTP client needs the token. Choosing the first
password is the setup wizard's first step, in `features/setup`.

Test it: there is no unit test; check it by hand with `make dev`, a wrong
password, and a stopped harness.
