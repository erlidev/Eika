/**
 * Who owns an Escape key press. Escape is the way out of a dialog, a select,
 * and a command palette, and it is how a text field cancels an input method's
 * composition. Only an Escape that nothing else wanted aborts the run.
 */

/** EscapeTarget is the part of a keyboard event the decision reads. */
export type EscapeTarget = {
  defaultPrevented: boolean;
  target: EventTarget | null;
};

/** overlays are the things that own Escape while they are on screen. */
const overlays = '[role="dialog"], [role="alertdialog"], [role="listbox"], [cmdk-root]';

/** editable matches a contenteditable host, which `[contenteditable=false]` is not. */
const editable = '[contenteditable]:not([contenteditable="false"])';

/**
 * abortsRun decides whether an Escape belongs to the run rather than to
 * whatever is on top of it. Radix calls `preventDefault` when it dismisses an
 * overlay, which covers most cases; the overlay query catches the rest, and a
 * focused text control keeps its own Escape.
 */
export function abortsRun(e: EscapeTarget): boolean {
  if (e.defaultPrevented) return false;
  if (document.querySelector(overlays) !== null) return false;
  const target = e.target;
  if (!(target instanceof HTMLElement)) return true;
  if (target.closest(editable) !== null) return false;
  const tag = target.tagName;
  return tag !== "INPUT" && tag !== "TEXTAREA" && tag !== "SELECT";
}
