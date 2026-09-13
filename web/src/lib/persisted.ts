/**
 * Small values the layout remembers between visits: pane widths and which
 * panel tab was open. Anything that outlives a reload and is not server state
 * goes through here, so there is one place that knows about localStorage.
 */

import { useCallback, useState } from "react";

/** readPersisted reads a stored number, falling back when it is not one. */
export function readPersistedNumber(key: string, fallback: number): number {
  try {
    const raw = localStorage.getItem(key);
    if (raw === null) return fallback;
    const value = Number.parseInt(raw, 10);
    return Number.isFinite(value) ? value : fallback;
  } catch {
    return fallback;
  }
}

/** readPersistedString reads a stored string, falling back when it is absent. */
export function readPersistedString(key: string, fallback: string): string {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

/** writePersisted stores a value, ignoring a browser that refuses. */
export function writePersisted(key: string, value: string | number): void {
  try {
    localStorage.setItem(key, String(value));
  } catch {
    // The value lasts for this page only.
  }
}

/**
 * usePersistedNumber is useState with a localStorage backing, for a pane
 * width. The initial read happens once, during the first render.
 */
export function usePersistedNumber(key: string, fallback: number): [number, (n: number) => void] {
  const [value, setValue] = useState(() => readPersistedNumber(key, fallback));
  const set = useCallback(
    (next: number) => {
      setValue(next);
      writePersisted(key, next);
    },
    [key],
  );
  return [value, set];
}

/** usePersistedString is useState with a localStorage backing, for a tab id. */
export function usePersistedString(key: string, fallback: string): [string, (s: string) => void] {
  const [value, setValue] = useState(() => readPersistedString(key, fallback));
  const set = useCallback(
    (next: string) => {
      setValue(next);
      writePersisted(key, next);
    },
    [key],
  );
  return [value, set];
}
