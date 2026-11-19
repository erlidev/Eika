/**
 * The pages the shell links to, in the order the rail and the bottom bar list
 * them. A page that no phase has built yet is absent, not disabled. Adding one
 * is a route in `App.tsx` and an entry here (docs/EXTENDING.md, "Adding a page").
 */

import { Activity, FolderKanban, MessageCircle } from "lucide-react";
import type { LucideIcon } from "lucide-react";

/** Page is one destination of the shell's navigation. */
export type Page = {
  /** id names the page; it is also the header's title when `title` is not enough. */
  id: string;
  /** title is the label under its icon and the header's heading. */
  title: string;
  /** path is its route. */
  path: string;
  icon: LucideIcon;
};

/** pages is every page the shell links to. */
export const pages: readonly Page[] = [
  { id: "chat", title: "Chat", path: "/", icon: MessageCircle },
  { id: "activity", title: "Activity", path: "/activity", icon: Activity },
  { id: "projects", title: "Projects", path: "/projects", icon: FolderKanban },
];

/** pageAt finds the page a location belongs to; a path under a page's own belongs to it. */
export function pageAt(pathname: string): Page | undefined {
  return pages.find(
    (page) => pathname === page.path || (page.path !== "/" && pathname.startsWith(`${page.path}/`)),
  );
}
