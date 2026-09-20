/**
 * The model thinking. Reasoning is not the answer, so it never competes with
 * it: by default the block is one quiet line that says what the model is
 * chewing on and opens on a click, the way a terminal agent shows it. A
 * reader who wants it all opens every block from the settings instead.
 */

import { Brain, ChevronRight } from "lucide-react";
import { useState } from "react";

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { useTranscriptPreferences } from "@/features/session/preferences";
import type { ReasoningItem } from "@/features/session/transcript";
import { cn } from "@/lib/utils";

export type ReasoningProps = {
  item: ReasoningItem;
};

/**
 * tail is the last thing the model said, which is what a one-line preview of
 * a block still being written should show. Reasoning arrives as loose prose,
 * so the last non-empty line is the closest thing it has to a current
 * thought.
 */
function tail(text: string): string {
  const lines = text.split("\n");
  for (let i = lines.length - 1; i >= 0; i -= 1) {
    const line = lines[i]?.trim() ?? "";
    if (line !== "") return line.replace(/^#+\s*/, "").replace(/\*\*/g, "");
  }
  return "";
}

export function Reasoning({ item }: ReasoningProps) {
  const preference = useTranscriptPreferences((s) => s.reasoning);
  // The preference decides until the reader says otherwise on this block,
  // which is why the chosen state starts empty rather than at the preference.
  const [chosen, setChosen] = useState<boolean | null>(null);
  const expanded = chosen ?? preference === "expanded";
  const preview = tail(item.text);

  return (
    <Collapsible open={expanded} onOpenChange={setChosen} className="text-muted-foreground min-w-0">
      <CollapsibleTrigger
        className={cn(
          "hover:bg-accent hover:text-foreground focus-visible:ring-ring flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-left transition-colors",
          "focus-visible:ring-1 focus-visible:outline-none",
        )}
      >
        <ChevronRight
          aria-hidden
          className={cn("size-3.5 shrink-0 transition-transform", expanded && "rotate-90")}
        />
        <Brain aria-hidden className={cn("size-3.5 shrink-0", item.streaming && "animate-pulse")} />
        <span className="shrink-0 text-xs font-medium">
          {item.streaming ? "Thinking" : "Thought"}
        </span>
        {!expanded && preview !== "" && (
          <span className="min-w-0 flex-1 truncate text-xs italic">{preview}</span>
        )}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="border-border/70 mt-1 ml-2.5 border-l-2 pl-3">
          <p className="text-xs leading-relaxed whitespace-pre-wrap">
            {item.text}
            {item.streaming && (
              <span
                aria-hidden
                className="bg-muted-foreground ml-0.5 inline-block h-3 w-1 animate-pulse align-text-bottom"
              />
            )}
          </p>
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
