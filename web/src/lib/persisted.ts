/**
 * Small values the browser remembers between visits: how the transcript
 * renders. Anything that outlives a reload and is not server state
 * goes through here, so there is one place that knows about localStorage.
 */

/** readPersistedString reads a stored string, falling back when it is absent. */
export function readPersistedString(key: string, fallback: string): string {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

/** writePersisted stores a value, ignoring a browser that refuses. */
export function writePersisted(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // The value lasts for this page only.
  }
}
