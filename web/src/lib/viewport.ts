/**
 * The visual viewport, for the one thing the layout viewport cannot say: how
 * much of the page the on-screen keyboard covers. Pure so it is tested
 * without a browser.
 */

/** keyboardMin is the smallest cover, in pixels, that counts as a keyboard; a browser's own toolbar is less. */
export const keyboardMin = 100;

/** ViewportBox is the part of `window.visualViewport` the keyboard changes. */
export type ViewportBox = {
  height: number;
  offsetTop: number;
  /** scale is above 1 while the page is pinch-zoomed, which shrinks the box without any keyboard. */
  scale: number;
};

/**
 * keyboardInset is how far the keyboard covers the bottom of the layout
 * viewport, which is `layoutHeight` tall. It is 0 where the browser resizes
 * the layout viewport itself (`interactive-widget=resizes-content`), since
 * the two heights then agree, and 0 while zoomed, which is no keyboard.
 */
export function keyboardInset(layoutHeight: number, viewport: ViewportBox): number {
  if (viewport.scale > 1.01) return 0;
  const covered = Math.round(layoutHeight - viewport.height - viewport.offsetTop);
  return covered >= keyboardMin ? covered : 0;
}

/**
 * keyboardOpen says whether an on-screen keyboard is up: the visual viewport
 * says so where it can, and on a touch screen a focused text field says so
 * where the browser resized the layout viewport instead.
 */
export function keyboardOpen(inset: number, touch: boolean, editing: boolean): boolean {
  return inset > 0 || (touch && editing);
}

/** isEditable says whether focus on `element` brings up a keyboard. */
export function isEditable(element: Element | null): boolean {
  if (element === null) return false;
  if (element instanceof HTMLTextAreaElement) return !element.readOnly;
  if (element instanceof HTMLInputElement) {
    return !["button", "checkbox", "radio", "range", "submit", "reset", "file", "image"].includes(
      element.type,
    );
  }
  return element.closest('[contenteditable=""], [contenteditable="true"]') !== null;
}
