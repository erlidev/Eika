/**
 * What the Context panel and the inspector share: the colours of a request's
 * parts, the meter of the context window, the bar of what fills it, and the
 * link from a part to the setting behind it.
 */

import { Pencil } from "lucide-react";

import type { ModelContext, SessionConfiguration } from "@/api/types";
import { Button } from "@/components/ui/button";
import { percent, segmentColors, windowUse } from "@/features/context/segments";
import type { EditTarget, Segment, SegmentKind } from "@/features/context/segments";
import { useConfigEditor } from "@/features/profiles/store";
import { formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

/** Dot is a part's colour, beside its name. */
export function Dot({ kind, className }: { kind: SegmentKind; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn("size-2 shrink-0 rounded-full", segmentColors[kind], className)}
    />
  );
}

export type WindowMeterProps = {
  context: ModelContext;
  /** total is the request's estimated size. */
  total: number;
};

/**
 * WindowMeter shows how the request fills its model's context window: what
 * it uses, the room its answer may take, and what is left.
 */
export function WindowMeter({ context, total }: WindowMeterProps) {
  const reserved = context.parameters.sampling.max_output ?? 0;
  const use = windowUse(total, reserved, context.context_window);
  if (context.context_window <= 0) return null;
  const width = (n: number) => `${String(Math.min(100, (n / use.window) * 100))}%`;
  return (
    <div className="space-y-1">
      <div
        role="meter"
        aria-label="Context window"
        aria-valuemin={0}
        aria-valuemax={use.window}
        aria-valuenow={Math.min(use.used, use.window)}
        aria-valuetext={`${formatTokens(use.used)} of ${formatTokens(use.window)} tokens, ${formatTokens(use.reserved)} kept for the answer`}
        className="bg-muted flex h-1.5 overflow-hidden rounded-full"
      >
        <span
          className={cn("h-full min-w-0.5", use.overflows ? "bg-destructive" : "bg-primary")}
          style={{ width: width(use.used) }}
        />
        <span className="bg-muted-foreground/25 h-full" style={{ width: width(use.reserved) }} />
      </div>
      <p className="text-muted-foreground flex flex-wrap gap-x-3 text-xs">
        <span className="flex items-center gap-1">
          <span aria-hidden className="bg-primary size-2 rounded-full" />
          <span className="text-foreground font-mono tabular-nums">
            {percent(use.used, use.window)}
          </span>
          of <span className="font-mono tabular-nums">{formatTokens(use.window)}</span> window
        </span>
        {use.reserved > 0 && (
          <span className="flex items-center gap-1">
            <span aria-hidden className="bg-muted-foreground/25 size-2 rounded-full" />
            <span className="font-mono tabular-nums">{formatTokens(use.reserved)}</span> kept for
            the answer
          </span>
        )}
        <span className={cn(use.overflows && "text-destructive")}>
          {use.overflows ? (
            "The request and its answer do not fit"
          ) : (
            <>
              <span className="font-mono tabular-nums">{formatTokens(use.free)}</span> free
            </>
          )}
        </span>
      </p>
    </div>
  );
}

export type PartsBarProps = {
  parts: readonly Segment[];
  total: number;
  onOpen: (kind: SegmentKind) => void;
  className?: string;
};

/** PartsBar is the request's parts side by side, each a button that opens it. */
export function PartsBar({ parts, total, onOpen, className }: PartsBarProps) {
  if (total <= 0) return null;
  return (
    <div
      className={cn("bg-muted flex h-3 gap-px overflow-hidden rounded-md", className)}
      role="group"
      aria-label="Tokens by part"
    >
      {parts.map((part) => (
        <button
          key={part.kind}
          type="button"
          title={`${part.label}: ~${formatTokens(part.tokens)} tokens`}
          aria-label={`${part.label}, about ${formatTokens(part.tokens)} tokens`}
          className={cn(
            "h-full min-w-1 transition-opacity hover:opacity-75 focus-visible:opacity-75 focus-visible:outline-none",
            segmentColors[part.kind],
          )}
          style={{ width: `${String((part.tokens / total) * 100)}%` }}
          onClick={() => {
            onOpen(part.kind);
          }}
        />
      ))}
    </div>
  );
}

export type EditLinkProps = {
  sessionId: string;
  config: SessionConfiguration | undefined;
  target: EditTarget;
  /** onOpen runs before the editor opens: the inspector closes, so the editor is not under it. */
  onOpen: () => void;
  /** what names the setting for a screen reader: "the temperature". */
  what: string;
  /** compact draws only the pencil. */
  compact?: boolean;
};

/**
 * EditLink opens the editor of the layer a value comes from, on the tab that
 * holds it: the profile's when the profile sets it, else the session's, where
 * it can be set over what it falls through to.
 */
export function EditLink({
  sessionId,
  config,
  target,
  onOpen,
  what,
  compact = false,
}: EditLinkProps) {
  const editSession = useConfigEditor((s) => s.editSession);
  const editProfile = useConfigEditor((s) => s.editProfile);
  if (config === undefined) return null;
  const resolved = config.resolved;
  const inProfile = resolved.sources?.[target.key] === "profile";
  const label = inProfile ? `Edit in ${resolved.profile_name}` : "Edit for this session";
  return (
    <Button
      type="button"
      size={compact ? "icon-xs" : "xs"}
      variant="ghost"
      title={compact ? label : undefined}
      aria-label={`${label}: ${what}`}
      onClick={() => {
        onOpen();
        if (inProfile) editProfile(resolved.profile_id, target.section);
        else editSession(sessionId, target.section);
      }}
    >
      <Pencil aria-hidden />
      {!compact && label}
    </Button>
  );
}
