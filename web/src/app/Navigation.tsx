/**
 * The shell's two navigations over the same pages: a rail down the left from
 * 640px, and a bar along the bottom under it. Each lists the pages and, last,
 * Settings, which opens the dialog rather than a page.
 */

import { Settings, Terminal } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { NavLink } from "react-router";

import { HaltSlot } from "@/app/HaltSlot";
import { pages } from "@/app/pages";
import { useSettingsDialog } from "@/features/settings";
import { cn } from "@/lib/utils";

/** railWidth is where the bottom bar gives way to the rail. */
export const railWidth = 640;

/** itemClass is one destination: its icon in a pill, which is filled while it is the page, and its label. */
const itemClass =
  "group flex min-w-0 flex-col items-center gap-0.5 rounded-md px-1 py-1 text-2xs text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-ring focus-visible:ring-1 focus-visible:outline-none aria-[current=page]:text-foreground";

type ItemIconProps = { icon: LucideIcon };

function ItemIcon({ icon: Icon }: ItemIconProps) {
  return (
    <span className="group-hover:bg-accent group-aria-[current=page]:bg-accent flex h-6 w-10 items-center justify-center rounded-full transition-colors">
      <Icon aria-hidden className="size-4" />
    </span>
  );
}

type PageLinksProps = { itemClassName: string };

function PageLinks({ itemClassName }: PageLinksProps) {
  return pages.map((page) => (
    <NavLink
      key={page.id}
      to={page.path}
      end={page.path === "/"}
      className={cn(itemClass, itemClassName)}
    >
      <ItemIcon icon={page.icon} />
      {page.title}
    </NavLink>
  ));
}

type SettingsButtonProps = { className?: string };

function SettingsButton({ className }: SettingsButtonProps) {
  const show = useSettingsDialog((s) => s.show);
  return (
    <button
      type="button"
      className={cn(itemClass, className)}
      onClick={() => {
        show();
      }}
    >
      <ItemIcon icon={Settings} />
      Settings
    </button>
  );
}

/** Rail is the navigation of a window 640px wide or more. */
export function Rail() {
  return (
    <div className="bg-card flex w-16 shrink-0 flex-col items-center gap-3 border-r py-2">
      <Terminal aria-label="Eika" role="img" className="size-4" />
      <nav aria-label="Pages" className="flex flex-col gap-1">
        <PageLinks itemClassName="w-14" />
      </nav>
      <div className="mt-auto flex flex-col items-center gap-1">
        <HaltSlot />
        <SettingsButton className="w-14" />
      </div>
    </div>
  );
}

/** BottomBar is the navigation of a phone: the pages and Settings along the bottom. */
export function BottomBar() {
  return (
    <nav aria-label="Pages" className="bg-card pb-safe flex shrink-0 items-stretch border-t px-1">
      <PageLinks itemClassName="flex-1" />
      <SettingsButton className="flex-1" />
    </nav>
  );
}
