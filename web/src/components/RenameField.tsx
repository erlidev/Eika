/**
 * A field that renames something in place: a sidebar row or the session
 * header swaps its label for it. Enter or leaving the field saves, Escape
 * keeps the old name; an empty or unchanged name saves nothing.
 */

import { useRef } from "react";

import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

/** maxNameLength matches the harness's bound on a title or a name. */
const maxNameLength = 200;

export type RenameFieldProps = {
  value: string;
  /** label names the field for a screen reader, such as "Rename Fix the login loop". */
  label: string;
  onSubmit: (name: string) => void;
  /**
   * onDone ends the renaming. `byKey` is true after Enter or Escape, when the
   * owner hands focus back to what started it; after a click elsewhere the
   * focus stays where the click put it.
   */
  onDone: (byKey: boolean) => void;
  className?: string;
};

export function RenameField({ value, label, onSubmit, onDone, className }: RenameFieldProps) {
  // Enter unmounts the field, and a browser may blur it on the way out; the
  // first of the two settles it.
  const settled = useRef(false);
  const finish = (next: string | null, byKey: boolean) => {
    if (settled.current) return;
    settled.current = true;
    const name = next?.trim() ?? "";
    if (name !== "" && name !== value) onSubmit(name);
    onDone(byKey);
  };
  return (
    <Input
      // The field exists because the user asked to rename; it has the focus
      // the moment it appears, with the old name selected to type over.
      autoFocus
      defaultValue={value}
      aria-label={label}
      maxLength={maxNameLength}
      onFocus={(e) => {
        e.currentTarget.select();
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          finish(e.currentTarget.value, true);
        } else if (e.key === "Escape") {
          // Escape ends the renaming, not the drawer or dialog around it.
          e.preventDefault();
          e.stopPropagation();
          finish(null, true);
        }
      }}
      onBlur={(e) => {
        finish(e.currentTarget.value, false);
      }}
      className={cn("h-6 rounded-md px-1.5 text-xs md:text-xs", className)}
    />
  );
}
