/**
 * A compaction in the transcript. Everything above it stays on screen, but
 * the model reads only the summary in its place from here on, so the row is a
 * divider across the conversation: one quiet line that says how much was
 * folded away, which opens on a click to the summary the model now reads.
 */

import { ChevronRight, FoldVertical, Loader2, TriangleAlert } from "lucide-react";
import { useState } from "react";

import { Markdown } from "@/components/Markdown";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import type { CompactionItem } from "@/features/session/transcript";
import { formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

export type CompactionProps = {
  item: CompactionItem;
};

/** reasonText says what started a compaction. */
const reasonText: Record<CompactionItem["reason"], string> = {
  manual: "on request",
  threshold: "automatically, as the context neared its window",
  overflow: "automatically, after the endpoint refused a request as too large",
};

/** headline is the one line the divider shows. */
function headline(item: CompactionItem): string {
  if (item.running) return `Compacting about ${formatTokens(item.tokensBefore)} tokens…`;
  if (item.error !== undefined) return "Compaction failed; the conversation is unchanged";
  const after = item.tokensAfter === undefined ? "" : ` to about ${formatTokens(item.tokensAfter)}`;
  return `Compacted about ${formatTokens(item.tokensBefore)} tokens${after}`;
}

export function Compaction({ item }: CompactionProps) {
  const [open, setOpen] = useState(false);
  const Icon = item.running ? Loader2 : item.error === undefined ? FoldVertical : TriangleAlert;
  const expandable = !item.running && (item.summary !== "" || item.error !== undefined);

  return (
    <Collapsible open={open} onOpenChange={setOpen} className="text-muted-foreground min-w-0">
      <div className="flex items-center gap-2">
        <span aria-hidden className="bg-border h-px flex-1" />
        <CollapsibleTrigger
          disabled={!expandable}
          title={`Compacted ${reasonText[item.reason]}`}
          className={cn(
            "flex max-w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs transition-colors",
            "hover:bg-accent hover:text-foreground focus-visible:ring-ring focus-visible:ring-1 focus-visible:outline-none",
            "disabled:hover:text-muted-foreground disabled:cursor-default disabled:hover:bg-transparent",
            item.error !== undefined && "text-destructive",
          )}
        >
          {expandable && (
            <ChevronRight
              aria-hidden
              className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")}
            />
          )}
          <Icon aria-hidden className={cn("size-3.5 shrink-0", item.running && "animate-spin")} />
          <span className="truncate font-medium">{headline(item)}</span>
        </CollapsibleTrigger>
        <span aria-hidden className="bg-border h-px flex-1" />
      </div>
      <CollapsibleContent>
        <div className="border-border/70 mt-1 ml-2.5 border-l-2 pl-3 text-sm">
          {item.error === undefined ? (
            <>
              <p className="mb-2 text-xs">
                The model reads this summary in place of the conversation above.
              </p>
              <Markdown>{item.summary}</Markdown>
            </>
          ) : (
            <p className="text-destructive text-xs break-words">{item.error}</p>
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
