/** The sidebar's rows: a row that folds what is under it, and a row's icon buttons. */

import { ChevronDown, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type RowProps = {
  depth: number;
  open: boolean;
  onToggle: () => void;
  icon: React.ReactNode;
  label: string;
  meta?: string;
  actions: React.ReactNode;
};

export function Row({ depth, open, onToggle, icon, label, meta, actions }: RowProps) {
  return (
    <div className="group hover:bg-accent flex items-center gap-1 rounded-md pr-1 transition-colors">
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        style={{ paddingLeft: `${String(depth * 12 + 4)}px` }}
        className="focus-visible:ring-ring flex min-w-0 flex-1 items-center gap-1.5 py-1 text-left text-xs focus-visible:ring-1 focus-visible:outline-none"
      >
        {open ? (
          <ChevronDown aria-hidden className="size-3.5 shrink-0" />
        ) : (
          <ChevronRight aria-hidden className="size-3.5 shrink-0" />
        )}
        {icon}
        <span className="truncate font-medium">{label}</span>
        {meta !== undefined && meta !== "" && (
          <span className="text-muted-foreground shrink-0 truncate font-mono text-2xs">{meta}</span>
        )}
      </button>
      <span className="flex shrink-0 gap-0.5 opacity-0 group-focus-within:opacity-100 group-hover:opacity-100">
        {actions}
      </span>
    </div>
  );
}

export function IconButton({
  label,
  children,
  onClick,
  disabled,
  destructive,
}: {
  label: string;
  children: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  destructive?: boolean;
}) {
  return (
    <Button
      size="icon-xs"
      variant="ghost"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
      className={cn(destructive && "hover:text-destructive")}
    >
      {children}
    </Button>
  );
}
