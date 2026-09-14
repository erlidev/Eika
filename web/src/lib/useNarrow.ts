/** Viewport width as a boolean, read through the media query it belongs to. */

import { useCallback, useSyncExternalStore } from "react";

/**
 * useNarrow reports whether the viewport is below `width` pixels. It uses
 * matchMedia rather than a resize listener, so it re-renders only when the
 * answer changes.
 */
export function useNarrow(width: number): boolean {
  const query = `(max-width: ${String(width - 1)}px)`;
  const subscribe = useCallback(
    (listener: () => void) => {
      const media = window.matchMedia(query);
      media.addEventListener("change", listener);
      return () => {
        media.removeEventListener("change", listener);
      };
    },
    [query],
  );
  return useSyncExternalStore(
    subscribe,
    () => window.matchMedia(query).matches,
    () => false,
  );
}
