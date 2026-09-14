/**
 * The deployment the UI talks to: its base URL and its bearer token. Both are
 * held in localStorage so a reload keeps the session, and both are cleared
 * when the harness answers 401, which sends the user back to the connect
 * screen.
 *
 * This is a plain module rather than a store because `api/` must not depend on
 * a feature; `features/connect` exposes it to React with useSyncExternalStore.
 */

const tokenKey = "eika.token";
const baseUrlKey = "eika.base_url";

/** Connection is the deployment the UI is pointed at. */
export type Connection = {
  /** baseUrl is the harness origin, empty for the page's own origin. */
  baseUrl: string;
  /** token is the deployment's bearer token, empty when not connected. */
  token: string;
};

const disconnected: Connection = { baseUrl: "", token: "" };

function read(): Connection {
  try {
    return {
      baseUrl: localStorage.getItem(baseUrlKey) ?? "",
      token: localStorage.getItem(tokenKey) ?? "",
    };
  } catch {
    // A browser with storage disabled still runs; it just cannot remember.
    return disconnected;
  }
}

let current: Connection = read();
const listeners = new Set<() => void>();

function publish(next: Connection): void {
  current = next;
  for (const listener of listeners) listener();
}

/** getConnection returns the deployment the UI is currently pointed at. */
export function getConnection(): Connection {
  return current;
}

/** isConnected reports whether a token has been entered. */
export function isConnected(): boolean {
  return current.token !== "";
}

/** subscribeConnection registers a listener and returns its unsubscribe. */
export function subscribeConnection(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** connect stores the token and base URL and points the UI at them. */
export function connect(next: Connection): void {
  try {
    localStorage.setItem(tokenKey, next.token);
    localStorage.setItem(baseUrlKey, next.baseUrl);
  } catch {
    // Keep going in memory; the connection is lost on reload.
  }
  publish(next);
}

/**
 * disconnect forgets the token. The client calls it on any 401, so a token
 * that the harness no longer accepts returns the user to the connect screen.
 */
export function disconnect(): void {
  try {
    localStorage.removeItem(tokenKey);
  } catch {
    // Nothing to forget.
  }
  publish({ baseUrl: current.baseUrl, token: "" });
}

/** resolveUrl resolves an API path against a base URL, which may be empty. */
export function resolveUrl(baseUrl: string, path: string): string {
  return baseUrl === "" ? path : `${baseUrl.replace(/\/+$/, "")}${path}`;
}

/** apiUrl resolves an API path against the configured base URL. */
export function apiUrl(path: string): string {
  return resolveUrl(current.baseUrl, path);
}

/**
 * eventStreamUrl builds the WebSocket URL of the event stream, carrying the
 * token as a query parameter because a browser cannot set a handshake header.
 */
export function eventStreamUrl(topics: readonly string[], since?: string): string {
  const base = current.baseUrl === "" ? window.location.origin : current.baseUrl;
  const url = new URL("/api/events", base);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.searchParams.set("token", current.token);
  if (topics.length > 0) url.searchParams.set("topics", topics.join(","));
  if (since !== undefined && since !== "") url.searchParams.set("since", since);
  return url.toString();
}
