/**
 * The two-pane layout of a list-and-detail page. From 1024px the list (20rem)
 * and the detail sit side by side; under it the route picks one, so a phone
 * goes from the list to the detail and back. The detail is always a route, so a
 * deep link works at both sizes.
 */

import { cn } from "@/lib/utils";

export type MasterDetailProps = {
  /** listLabel names the list's landmark for a screen reader. */
  listLabel: string;
  list: React.ReactNode;
  detail: React.ReactNode;
  /** open is true when the route names a detail: a phone then shows it instead of the list. */
  open: boolean;
};

export function MasterDetail({ listLabel, list, detail, open }: MasterDetailProps) {
  return (
    <div className="flex min-h-0 flex-1">
      <nav
        aria-label={listLabel}
        className={cn(
          "min-h-0 min-w-0 flex-1 flex-col lg:flex lg:w-80 lg:flex-none lg:border-r",
          open ? "hidden" : "flex",
        )}
      >
        {list}
      </nav>
      <div className={cn("min-h-0 min-w-0 flex-1 flex-col lg:flex", open ? "flex" : "hidden")}>
        {detail}
      </div>
    </div>
  );
}
