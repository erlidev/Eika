/**
 * The shell of a standalone page shown instead of the workbench: sign-in,
 * the setup wizard, and the load failure. Every such page is one bordered
 * panel on a muted backdrop, with the same padding, heading, and mark, so
 * they read as one family (docs/STYLE_GUIDE.md, Web UI design).
 */

import { cn } from "@/lib/utils";

export type ScreenProps = {
  /** wide is for a page with a step list beside its content; the default fits one form. */
  wide?: boolean;
  /** padded is false when the page lays out its own padded columns. */
  padded?: boolean;
  role?: "alert";
  className?: string;
  children: React.ReactNode;
};

export function Screen({ wide = false, padded = true, role, className, children }: ScreenProps) {
  return (
    <main className="bg-muted/30 text-foreground flex min-h-screen items-start justify-center p-4 sm:items-center sm:p-8">
      <div
        role={role}
        className={cn(
          "bg-background w-full overflow-hidden rounded-md border",
          wide ? "max-w-4xl" : "max-w-sm",
          padded && "p-6 sm:p-8",
          className,
        )}
      >
        {children}
      </div>
    </main>
  );
}

export type ScreenMarkProps = {
  icon: React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>;
  /** tone "error" marks a page that reports a failure. */
  tone?: "brand" | "error";
};

/** ScreenMark is the square icon above a screen's heading. */
export function ScreenMark({ icon: Icon, tone = "brand" }: ScreenMarkProps) {
  return (
    <span
      className={cn(
        "inline-flex size-9 items-center justify-center rounded-md",
        tone === "brand"
          ? "bg-primary text-primary-foreground"
          : "bg-destructive/10 text-destructive",
      )}
    >
      <Icon aria-hidden className="size-4" />
    </span>
  );
}

export type ScreenHeaderProps = {
  /** mark is a ScreenMark above the title, if the page has one. */
  mark?: React.ReactNode;
  title: string;
  /** children is the sentence under the title. */
  children?: React.ReactNode;
};

/** ScreenHeader is a screen's mark, title, and the sentence under it. */
export function ScreenHeader({ mark, title, children }: ScreenHeaderProps) {
  return (
    <div className="space-y-2">
      {mark}
      <h1 className="text-xl font-semibold">{title}</h1>
      {children !== undefined && (
        <div className="text-muted-foreground max-w-prose text-sm">{children}</div>
      )}
    </div>
  );
}
