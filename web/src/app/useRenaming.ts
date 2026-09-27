/** A sidebar row's rename state. */

import { useRef, useState } from "react";

/** Renaming is a row's rename state, from `useRenaming`. */
export type Renaming = ReturnType<typeof useRenaming>;

/**
 * useRenaming holds whether a row is being renamed, and gives the focus back
 * to the row's label when a rename ends from the keyboard.
 */
export function useRenaming() {
  const [active, setActive] = useState(false);
  const refocus = useRef(false);
  return {
    active,
    start: () => {
      setActive(true);
    },
    stop: (byKey: boolean) => {
      refocus.current = byKey;
      setActive(false);
    },
    /** labelRef goes on the row's label, which comes back when the field goes. */
    labelRef: (el: HTMLButtonElement | null) => {
      if (el !== null && refocus.current) {
        refocus.current = false;
        el.focus();
      }
    },
  };
}
