/**
 * Where the halt button goes: the bottom of the rail and the phone's header,
 * so it is one press away on every page.
 */

import { HaltButton } from "@/features/halt";

export type HaltSlotProps = {
  layout: "rail" | "header";
};

export function HaltSlot({ layout }: HaltSlotProps) {
  return (
    <div data-slot="halt" className="shrink-0">
      <HaltButton layout={layout} />
    </div>
  );
}
