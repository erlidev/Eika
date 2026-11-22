/**
 * The app shell every page lives in: a rail down the left from 640px and a
 * bottom bar under it, a header with the page's title, and the page below.
 * It fills the dynamic viewport, leaves room for the device's safe areas, and
 * keeps the page above the on-screen keyboard, so the composer is never
 * covered. Settings stays the one dialog it opens. The halt button is in the
 * rail and in the phone's header, and the halt banner sits under the header.
 */

import { Outlet, useLocation } from "react-router";

import { HaltSlot } from "@/app/HaltSlot";
import { BottomBar, Rail, railWidth } from "@/app/Navigation";
import { pageAt } from "@/app/pages";
import { ThemeToggle } from "@/app/ThemeToggle";
import { ImageViewer } from "@/components/MessageImages";
import { HaltBanner, useHaltEvents } from "@/features/halt";
import { Toasts } from "@/features/notifications";
import { useMCPEvents } from "@/features/mcp";
import { useSessionUpdates } from "@/features/sessions";
import { SettingsDialog } from "@/features/settings";
import { useNarrow } from "@/lib/useNarrow";
import { useViewportAnchor } from "@/lib/useViewportAnchor";
import { cn } from "@/lib/utils";

export function Shell() {
  const page = pageAt(useLocation().pathname);
  const phone = useNarrow(railWidth);
  const keyboardUp = useViewportAnchor();
  // The MCP servers change on their own, as they connect, drop, and list
  // new tools, and a session is named in the background; one subscription
  // each for the whole shell keeps the settings and the titles current.
  useMCPEvents();
  useHaltEvents();
  useSessionUpdates();

  // The bar stands under the page and gives way to the keyboard, which the
  // page then sits on; without a bar the page leaves room for the device.
  const bar = phone && !keyboardUp;

  return (
    // The shell is the whole window and each page scrolls inside itself, so
    // it clips: nothing may scroll the page out from under the navigation.
    <div className="bg-background text-foreground pt-safe px-safe flex h-dvh overflow-hidden">
      {!phone && <Rail />}
      <div className={cn("flex min-w-0 flex-1 flex-col", !bar && "pb-viewport")}>
        <header className="flex h-10 shrink-0 [@media(pointer:coarse)]:min-h-[48px] items-center gap-2 border-b px-3">
          <h1 className="text-sm font-semibold">{page?.title ?? "Eika"}</h1>
          <span className="ml-auto flex items-center gap-1">
            {phone && <HaltSlot layout="header" />}
            <ThemeToggle />
          </span>
        </header>
        <HaltBanner />
        <main className="relative flex min-h-0 flex-1 flex-col">
          <Outlet />
          <Toasts />
        </main>
        {bar && <BottomBar />}
      </div>
      <SettingsDialog />
      <ImageViewer />
    </div>
  );
}
