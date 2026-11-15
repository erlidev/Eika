/**
 * What the Context panel and the inspector share: the colours of a request's
 * parts, the meter of the context window, the bar of what fills it, and the
 * link from a part to the setting behind it.
 */

import { Pencil } from "lucide-react";

import type { ModelContext, SessionConfiguration } from "@/api/types";
import { Button } from "@/components/ui/button";
import { percent, segmentColors, tilde, windowUse } from "@/features/context/segments";
import type { EditTarget, Segment, SegmentKind } from "@/features/context/segments";
import { useSettingsDialog } from "@/features/settings/store";
import { formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

type DotProps = { kind: SegmentKind; className?: string };

/** Dot is a part's colour, beside its name. */
export function Dot({ kind, className }: DotProps) {
  return (
    <span
      aria-hidden
      className={cn("size-2 shrink-0 rounded-full", segmentColors[kind], className)}
    />
  );
}

export type WindowMeterProps = {
  context: ModelContext;
  /** used is the size of the request, in tokens. */
  used: number;
};

/**
 * WindowMeter shows how the request fills its model's context window: what
 * it uses, the room its answer may take, and what is left.
 */
export function WindowMeter({ context, used }: WindowMeterProps) {
  const reserved = context.parameters.sampling.max_output ?? 0;
  const use = windowUse(used, reserved, context.context_window);
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

/**
 * PartsBar is the request's parts side by side, each a button that opens it.
 * What the template adds is drawn too, as a plain segment with nothing to
 * open, and a remainder below zero is not drawn: it has no width to give.
 */
export function PartsBar({ parts, total, onOpen, className }: PartsBarProps) {
  if (total <= 0) return null;
  return (
    <div
      className={cn("bg-muted flex h-3 gap-px overflow-hidden rounded-md", className)}
      role="group"
      aria-label="Tokens by part"
    >
      {parts
        .filter((part) => part.tokens > 0)
        .map((part) => {
          const text = `${tilde(part.approximate)}${formatTokens(part.tokens)} tokens`;
          const style = { width: `${String((part.tokens / total) * 100)}%` };
          if (part.kind === "template") {
            return (
              <span
                key={part.kind}
                role="img"
                title={`${part.label}: ${text}`}
                aria-label={`${part.label}, ${text}`}
                className={cn("h-full min-w-1", segmentColors[part.kind])}
                style={style}
              />
            );
          }
          return (
            <button
              key={part.kind}
              type="button"
              title={`${part.label}: ${text}`}
              aria-label={`${part.label}, ${text}`}
              className={cn(
                "h-full min-w-1 transition-opacity hover:opacity-75 focus-visible:opacity-75 focus-visible:outline-none",
                segmentColors[part.kind],
              )}
              style={style}
              onClick={() => {
                onOpen(part.kind);
              }}
            />
          );
        })}
    </div>
  );
}

export type EditLinkProps = {
  config: SessionConfiguration | undefined;
  target: EditTarget;
  /** onOpen runs before the settings open: the inspector closes, so they are not under it. */
  onOpen: () => void;
  /** what names the setting for a screen reader: "the temperature". */
  what: string;
  /** compact draws only the pencil. */
  compact?: boolean;
};

/**
 * EditLink opens the Settings tab of the layer a value comes from: Agents
 * for a value the agent sets, General for a model its class picks. A value
 * from a layer with no editor, such as a legacy profile, has no link.
 */
export function EditLink({ config, target, onOpen, what, compact = false }: EditLinkProps) {
  const show = useSettingsDialog((s) => s.show);
  if (config === undefined) return null;
  const source = config.resolved.sources?.[target.key];
  if (source !== "agent" && source !== "class") return null;
  const label = source === "agent" ? "Edit in Agents" : "Edit the model classes";
  return (
    <Button
      type="button"
      size={compact ? "icon-xs" : "xs"}
      variant="ghost"
      title={compact ? label : undefined}
      aria-label={`${label}: ${what}`}
      onClick={() => {
        onOpen();
        show(source === "agent" ? "agents" : "general");
      }}
    >
      <Pencil aria-hidden />
      {!compact && label}
    </Button>
  );
}
