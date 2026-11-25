/**
 * The shell's two navigations over the same pages: a rail down the left from
 * 640px, and a bar along the bottom under it. The rail lists
 * every page and, last, Settings, which opens the dialog rather than a page.
 * The bar lists Chat, Activity and Inbox, then More: a sheet with the other
 * pages and Settings.
 */

import { Ellipsis, Settings, Terminal } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useState } from "react";
import { NavLink, useLocation } from "react-router";

import { HaltSlot } from "@/app/HaltSlot";
import { pageAt, pages } from "@/app/pages";
import type { Page } from "@/app/pages";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { useInboxCount } from "@/features/inbox";
import { useSettingsDialog } from "@/features/settings";
import { cn } from "@/lib/utils";

/** railWidth is where the bottom bar gives way to the rail. */
export const railWidth = 640;

/** itemClass is one destination: its icon in a pill, which is filled while it is the page, and its label. */
const itemClass =
  "group flex min-w-0 flex-col items-center gap-0.5 rounded-md px-1 py-1 text-2xs text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-ring focus-visible:ring-1 focus-visible:outline-none aria-[current=page]:text-foreground";

type ItemIconProps = { icon: LucideIcon; badge?: number };

function ItemIcon({ icon: Icon, badge = 0 }: ItemIconProps) {
  return (
    <span className="group-hover:bg-accent group-aria-[current=page]:bg-accent relative flex h-6 w-10 items-center justify-center rounded-full transition-colors">
      <Icon aria-hidden className="size-4" />
      {badge > 0 && (
        <span
          aria-hidden
          data-slot="badge"
          className="bg-primary text-primary-foreground absolute -top-1 right-0 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-2xs leading-none font-medium"
        >
          {badge > 99 ? "99+" : badge}
        </span>
      )}
    </span>
  );
}

/** useBadge is the count a page shows: the Inbox's unanswered items. Nothing else badges. */
function useBadge(page: Page): number {
  const inbox = useInboxCount();
  return page.id === "inbox" ? (inbox.data?.pending ?? 0) : 0;
}

type PageLinkProps = { page: Page; className?: string; onNavigate?: () => void };

function PageLink({ page, className, onNavigate }: PageLinkProps) {
  const badge = useBadge(page);
  return (
    <NavLink
      to={page.path}
      end={page.path === "/"}
      className={cn(itemClass, className)}
      onClick={onNavigate}
    >
      <ItemIcon icon={page.icon} badge={badge} />
      {page.title}
      {badge > 0 && <span className="sr-only">, {badge} unanswered</span>}
    </NavLink>
  );
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
        {pages.map((page) => (
          <PageLink key={page.id} page={page} className="w-14" />
        ))}
      </nav>
      <div className="mt-auto flex flex-col items-center gap-1">
        <HaltSlot layout="rail" />
        <SettingsButton className="w-14" />
      </div>
    </div>
  );
}

/** MoreSheet is what the bar has no room for: the other pages and Settings. */
function MoreSheet() {
  const [open, setOpen] = useState(false);
  const showSettings = useSettingsDialog((s) => s.show);
  const inMore = pageAt(useLocation().pathname)?.bar === false;
  const close = () => {
    setOpen(false);
  };
  return (
    <>
      <button
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        {...(inMore ? { "aria-current": "page" as const } : {})}
        className={cn(itemClass, "flex-1")}
        onClick={() => {
          setOpen(true);
        }}
      >
        <ItemIcon icon={Ellipsis} />
        More
      </button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          showCloseButton={false}
          className="pb-safe top-auto bottom-0 left-0 max-w-full translate-x-0 translate-y-0 rounded-md sm:max-w-full"
        >
          <DialogTitle className="sr-only">More</DialogTitle>
          <DialogDescription className="sr-only">The other pages, and Settings.</DialogDescription>
          <nav aria-label="More pages" className="grid grid-cols-4 gap-1">
            {pages
              .filter((page) => !page.bar)
              .map((page) => (
                <PageLink key={page.id} page={page} onNavigate={close} />
              ))}
            <button
              type="button"
              className={itemClass}
              onClick={() => {
                close();
                showSettings();
              }}
            >
              <ItemIcon icon={Settings} />
              Settings
            </button>
          </nav>
        </DialogContent>
      </Dialog>
    </>
  );
}

/** BottomBar is the navigation of a phone: three pages and More along the bottom. */
export function BottomBar() {
  return (
    <nav aria-label="Pages" className="bg-card pb-safe flex shrink-0 items-stretch border-t px-1">
      {pages
        .filter((page) => page.bar)
        .map((page) => (
          <PageLink key={page.id} page={page} className="flex-1" />
        ))}
      <MoreSheet />
    </nav>
  );
}
