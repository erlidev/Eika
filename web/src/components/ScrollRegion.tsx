/** A bounded region that can be scrolled with a keyboard. */

import type { KeyboardEvent, ReactNode } from "react";

import { cn } from "@/lib/utils";

type ScrollRegionProps = {
  label: string;
  children: ReactNode;
  className?: string;
};

export function ScrollRegion({ label, children, className }: ScrollRegionProps) {
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget) return;
    if (event.key !== "Home" && event.key !== "End") return;
    event.preventDefault();
    event.currentTarget.scrollTop = event.key === "Home" ? 0 : event.currentTarget.scrollHeight;
  };
  return (
    <div
      role="region"
      aria-label={label}
      tabIndex={0}
      onKeyDown={onKeyDown}
      className={cn(
        "focus-visible:ring-ring overflow-auto focus-visible:ring-1 focus-visible:outline-none",
        className,
      )}
    >
      {children}
    </div>
  );
}
