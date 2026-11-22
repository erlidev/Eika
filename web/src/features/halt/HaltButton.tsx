/**
 * The halt button: always in the shell, one press, no confirmation. It sits at
 * the bottom of the rail and in the phone's header. Halting twice changes
 * nothing, so the button stays pressable while halted.
 */

import { OctagonX } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useHaltNow } from "@/features/halt/queries";
import { cn } from "@/lib/utils";

export type HaltButtonProps = {
  /** layout is where it sits: the rail stacks an icon over a label, the header is one row. */
  layout: "rail" | "header";
  className?: string;
};

export function HaltButton({ layout, className }: HaltButtonProps) {
  const halt = useHaltNow();
  return (
    <Button
      type="button"
      variant="outline"
      aria-label="Halt Eika"
      title="Halt Eika: stop every run now"
      className={cn(
        "border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive dark:border-destructive/50",
        layout === "rail" ? "h-auto w-14 flex-col gap-0.5 px-1 py-1 text-2xs" : "h-7 px-2 text-xs",
        className,
      )}
      onClick={() => {
        halt.mutate();
      }}
    >
      <OctagonX aria-hidden className={layout === "rail" ? "size-4" : "size-3.5"} />
      Halt
    </Button>
  );
}
