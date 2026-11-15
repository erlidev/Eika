/** The dot of a subagent's state, in the colour the design gives that state. */

import type { SessionState } from "@/api/types";
import { stateDot } from "@/features/activity/state";
import { cn } from "@/lib/utils";

export type StateDotProps = { state: SessionState; className?: string };

/** StateDot is decoration: whatever shows it also says the state in words. */
export function StateDot({ state, className }: StateDotProps) {
  return (
    <span aria-hidden className={cn("size-2 shrink-0 rounded-full", stateDot[state], className)} />
  );
}
