/**
 * A two-column split with a draggable divider. A pointer-events handler is
 * twenty lines and needs no dependency; the divider is also a real separator
 * with arrow-key support, which a library would have to be configured into
 * anyway.
 */

import { useCallback, useRef, useState } from "react";

import { cn } from "@/lib/utils";

/** SplitSide says which column the stored width belongs to. */
export type SplitSide = "start" | "end";

export type ResizableSplitProps = {
  /** side is the column whose width is fixed; the other one takes the rest. */
  side: SplitSide;
  /** width is the fixed column's width in pixels. */
  width: number;
  /** onWidthChange reports a new width as the divider moves. */
  onWidthChange: (width: number) => void;
  min: number;
  max: number;
  /** label names the divider for a screen reader. */
  label: string;
  /** panel is the fixed column. */
  panel: React.ReactNode;
  /** children fill the remaining space. */
  children: React.ReactNode;
  className?: string;
};

/** keyboardStep is how far an arrow key moves the divider. */
const keyboardStep = 24;

export function ResizableSplit({
  side,
  width,
  onWidthChange,
  min,
  max,
  label,
  panel,
  children,
  className,
}: ResizableSplitProps) {
  const container = useRef<HTMLDivElement>(null);
  const [dragging, setDragging] = useState(false);

  const clamp = useCallback((value: number) => Math.min(max, Math.max(min, value)), [min, max]);

  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (!dragging) return;
      const box = container.current?.getBoundingClientRect();
      if (!box) return;
      const next = side === "start" ? e.clientX - box.left : box.right - e.clientX;
      onWidthChange(clamp(Math.round(next)));
    },
    [dragging, side, onWidthChange, clamp],
  );

  const divider = (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={width}
      aria-valuemin={min}
      aria-valuemax={max}
      tabIndex={0}
      className={cn(
        "bg-border hover:bg-ring focus-visible:bg-ring relative w-px shrink-0 cursor-col-resize transition-colors",
        "after:absolute after:inset-y-0 after:-left-1 after:-right-1 after:content-['']",
        dragging && "bg-ring",
      )}
      onPointerDown={(e) => {
        e.currentTarget.setPointerCapture(e.pointerId);
        setDragging(true);
      }}
      onPointerUp={(e) => {
        e.currentTarget.releasePointerCapture(e.pointerId);
        setDragging(false);
      }}
      onKeyDown={(e) => {
        const towardsPanel = side === "start" ? "ArrowRight" : "ArrowLeft";
        const awayFromPanel = side === "start" ? "ArrowLeft" : "ArrowRight";
        if (e.key === towardsPanel) onWidthChange(clamp(width + keyboardStep));
        else if (e.key === awayFromPanel) onWidthChange(clamp(width - keyboardStep));
        else return;
        e.preventDefault();
      }}
    />
  );

  const fixed = (
    <div className="min-w-0 shrink-0 overflow-hidden" style={{ width: `${String(width)}px` }}>
      {panel}
    </div>
  );

  return (
    <div
      ref={container}
      className={cn("flex min-h-0 min-w-0 flex-1", className)}
      onPointerMove={onPointerMove}
    >
      {side === "start" ? (
        <>
          {fixed}
          {divider}
          <div className="flex min-w-0 flex-1 flex-col">{children}</div>
        </>
      ) : (
        <>
          <div className="flex min-w-0 flex-1 flex-col">{children}</div>
          {divider}
          {fixed}
        </>
      )}
    </div>
  );
}
