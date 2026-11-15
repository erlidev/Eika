/**
 * Where the halt button goes. The shell reserves it, in the rail and in the
 * phone's header, so phase 4 mounts the button without moving anything. Until
 * then the slot is empty and takes no room.
 */

import { cn } from "@/lib/utils";

export type HaltSlotProps = {
  className?: string;
};

export function HaltSlot({ className }: HaltSlotProps) {
  return <div data-slot="halt" className={cn("shrink-0 empty:hidden", className)} />;
}
