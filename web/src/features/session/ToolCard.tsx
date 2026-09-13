/**
 * One tool call, as a collapsible card. The header is the same for every
 * tool; the summary line and the body come from the tool's renderer.
 */

import { ChevronRight, CircleAlert, CircleCheck, Loader2 } from "lucide-react";
import { useState } from "react";

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { rendererFor } from "@/features/session/renderers/renderers";
import type { ToolItem } from "@/features/session/transcript";
import { formatDuration } from "@/lib/format";
import { cn } from "@/lib/utils";

export type ToolCardProps = {
  call: ToolItem;
  /**
   * open expands the card, which a question waiting for an answer needs. It is
   * not the state: a card the user has opened or closed keeps their choice,
   * and `open` decides only until then. A `tool.call` arrives before the
   * `question.asked` that follows it, so this has to react to the change.
   */
  open?: boolean;
};

export function ToolCard({ call, open = false }: ToolCardProps) {
  const [chosen, setChosen] = useState<boolean | null>(null);
  const expanded = chosen ?? open;
  const renderer = rendererFor(call.name);
  const summary = renderer.summary(call);

  return (
    <Collapsible
      open={expanded}
      onOpenChange={setChosen}
      className={cn(
        "bg-card overflow-hidden rounded-md border",
        call.isError && "border-destructive/40",
      )}
    >
      <CollapsibleTrigger
        className={cn(
          "hover:bg-accent/50 focus-visible:ring-ring flex w-full items-center gap-2 px-2 py-1.5 text-left",
          "focus-visible:ring-1 focus-visible:outline-none",
        )}
      >
        <ChevronRight
          aria-hidden
          className={cn("size-3.5 shrink-0 transition-transform", expanded && "rotate-90")}
        />
        <span className="font-mono text-xs font-semibold">{call.name}</span>
        <span className="text-muted-foreground min-w-0 flex-1 truncate font-mono text-xs">
          {summary}
        </span>
        {call.durationMs !== undefined && (
          <span className="text-muted-foreground shrink-0 font-mono text-[0.7rem] tabular-nums">
            {formatDuration(call.durationMs)}
          </span>
        )}
        <StatusIcon call={call} />
      </CollapsibleTrigger>
      <CollapsibleContent className="border-t p-2">
        <renderer.Body call={call} />
      </CollapsibleContent>
    </Collapsible>
  );
}

function StatusIcon({ call }: { call: ToolItem }) {
  if (!call.done) {
    return (
      <Loader2
        aria-label="running"
        className="text-muted-foreground size-3.5 shrink-0 animate-spin"
      />
    );
  }
  if (call.isError) {
    return <CircleAlert aria-label="failed" className="text-destructive size-3.5 shrink-0" />;
  }
  return <CircleCheck aria-label="succeeded" className="text-muted-foreground size-3.5 shrink-0" />;
}
