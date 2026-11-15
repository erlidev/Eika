/**
 * Keeps the layout on what the person can see of the page. It writes the
 * keyboard's cover to `--keyboard-inset` on the root element, which the shell
 * pads its bottom with, so the composer sits on top of the keyboard where the
 * browser does not move it, and reports whether a keyboard is up so the
 * bottom bar can give way.
 */

import { useEffect, useState } from "react";

import { isEditable, keyboardInset, keyboardOpen } from "@/lib/viewport";

/** useViewportAnchor returns whether an on-screen keyboard is up. */
export function useViewportAnchor(): boolean {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const viewport = window.visualViewport;
    const root = document.documentElement;
    const touch = window.matchMedia("(pointer: coarse)");
    let editing = isEditable(document.activeElement);
    const update = () => {
      const inset =
        viewport === null
          ? 0
          : keyboardInset(window.innerHeight, {
              height: viewport.height,
              offsetTop: viewport.offsetTop,
              scale: viewport.scale,
            });
      root.style.setProperty("--keyboard-inset", `${String(inset)}px`);
      setOpen(keyboardOpen(inset, touch.matches, editing));
    };
    // Focus has not moved yet when focusout fires, so the field it moves to
    // is the event's related target.
    const onFocusIn = (e: FocusEvent) => {
      editing = e.target instanceof Element && isEditable(e.target);
      update();
    };
    const onFocusOut = (e: FocusEvent) => {
      editing = e.relatedTarget instanceof Element && isEditable(e.relatedTarget);
      update();
    };
    update();
    viewport?.addEventListener("resize", update);
    viewport?.addEventListener("scroll", update);
    document.addEventListener("focusin", onFocusIn);
    document.addEventListener("focusout", onFocusOut);
    return () => {
      viewport?.removeEventListener("resize", update);
      viewport?.removeEventListener("scroll", update);
      document.removeEventListener("focusin", onFocusIn);
      document.removeEventListener("focusout", onFocusOut);
      root.style.removeProperty("--keyboard-inset");
    };
  }, []);

  return open;
}
