/**
 * A short status message in the colour of how things went: a hint, a
 * success, a failure, or work in progress. Forms use it for what the harness
 * answered, so every screen reports results the same way. A failure the
 * user can try again carries its Retry button as the action.
 */

import { CircleAlert, CircleCheck, Info, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { failureText } from "@/lib/failure";
import { cn } from "@/lib/utils";

export type NoticeTone = "info" | "success" | "error" | "pending";

export type NoticeProps = {
  tone?: NoticeTone;
  children: React.ReactNode;
  className?: string;
  /** action is a way forward shown beside the message, such as a Retry button. */
  action?: React.ReactNode;
};

const icons = { info: Info, success: CircleCheck, error: CircleAlert, pending: Loader2 };

const tones: Record<NoticeTone, string> = {
  info: "bg-muted/50 text-muted-foreground",
  pending: "bg-muted/50 text-muted-foreground",
  success: "border-success/25 bg-success/5 text-success",
  error: "border-destructive/30 bg-destructive/5 text-destructive",
};

export function Notice({ tone = "info", children, className, action }: NoticeProps) {
  const Icon = icons[tone];
  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      className={cn(
        "flex items-start gap-2 rounded-md border px-3 py-2 text-xs leading-relaxed",
        tones[tone],
        className,
      )}
    >
      <Icon
        aria-hidden
        className={cn("mt-0.5 size-3.5 shrink-0", tone === "pending" && "animate-spin")}
      />
      <div className="min-w-0 flex-1 break-words">{children}</div>
      {action !== undefined && <div className="-my-1 shrink-0">{action}</div>}
    </div>
  );
}

export type LoadErrorProps = {
  /** what names what failed to load, such as "the providers". */
  what: string;
  error: Error;
  /** retry loads it again; retrying disables the button meanwhile. */
  retry: () => void;
  retrying?: boolean;
  className?: string;
};

/**
 * LoadError is a failed read: what could not be loaded, why, and a Retry
 * button, so a failure never passes for an empty list.
 */
export function LoadError({ what, error, retry, retrying = false, className }: LoadErrorProps) {
  return (
    <Notice
      tone="error"
      {...(className === undefined ? {} : { className })}
      action={
        <Button type="button" size="xs" variant="outline" disabled={retrying} onClick={retry}>
          {retrying ? "Retrying…" : "Retry"}
        </Button>
      }
    >
      {failureText(`load ${what}`, error)}
    </Notice>
  );
}

export type ActionErrorProps = {
  /** action is what the user asked for, as a verb phrase: "save the order". */
  action: string;
  error: unknown;
  className?: string;
};

/**
 * ActionError is a failed write or action, shown beside the control that
 * started it: which action failed and what actually went wrong.
 */
export function ActionError({ action, error, className }: ActionErrorProps) {
  return (
    <Notice tone="error" {...(className === undefined ? {} : { className })}>
      {failureText(action, error)}
    </Notice>
  );
}
