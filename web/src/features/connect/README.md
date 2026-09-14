# connect

The screen that asks for the deployment's bearer token, and React access to
the token the browser stores.

- `ConnectScreen.tsx` stores the token with `api/connection`, then calls one
  real route to check it. A `401` clears the token again and the screen says
  so, which is the same path any later `401` takes.
- `useConnection.ts` reads `api/connection` through `useSyncExternalStore`,
  so the app re-renders when the token appears or is forgotten.

The token itself lives in `api/connection`, not here: `api/` must not depend
on a feature, and the HTTP client needs the token.

Test it: there is no test; the behaviour is one form over
`api/connection`, which `api/stream.test.ts` exercises indirectly. Check it by
hand with `make dev` and a wrong token.
