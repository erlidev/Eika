/**
 * What the authorization server sent the browser back with, read from the
 * callback page's query string as the harness wants it.
 */

import type { OAuthCallback } from "@/api/types";

/**
 * callbackFromQuery reads the redirect's parameters, or null when there is
 * no `state` and so nothing to finish. `iss` is carried exactly when the
 * redirect had one: the harness tells an absent issuer from an empty one.
 */
export function callbackFromQuery(search: string): OAuthCallback | null {
  const q = new URLSearchParams(search);
  const state = q.get("state");
  if (state === null || state === "") return null;
  const iss = q.get("iss");
  const error = q.get("error");
  const description = q.get("error_description");
  return {
    state,
    code: q.get("code") ?? "",
    ...(iss === null ? {} : { iss }),
    ...(error === null ? {} : { error }),
    ...(description === null ? {} : { error_description: description }),
  };
}
