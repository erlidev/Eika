/** The small coloured labels of a memory: its type, its state and a pin. */

import { Pin } from "lucide-react";

import type { MemoryState, MemoryType } from "@/api/types";
import { stateLabel, typeLabel } from "@/features/memory/state";
import { cn } from "@/lib/utils";

const chip = "inline-flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 text-2xs";

export function TypeChip({ type }: { type: MemoryType }) {
  return <span className={cn(chip, "text-muted-foreground")}>{typeLabel[type]}</span>;
}

/** StateChip marks a memory that is not active: pending in warning, archived quiet. */
export function StateChip({ state }: { state: MemoryState }) {
  if (state === "active") return null;
  return (
    <span
      className={cn(
        chip,
        state === "pending"
          ? "border-warning/30 bg-warning/5 text-warning"
          : "bg-muted/50 text-muted-foreground",
      )}
    >
      {stateLabel[state]}
    </span>
  );
}

export function PinMark() {
  return (
    <span className={cn(chip, "text-muted-foreground")}>
      <Pin aria-hidden className="size-3" />
      Pinned
    </span>
  );
}
