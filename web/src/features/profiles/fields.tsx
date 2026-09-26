/**
 * The pieces every field of the configuration editor is built from: the
 * header with the label and a chip that says where the value comes from, the
 * reset that unsets a value, and a switch between two values that can also
 * be left to fall through.
 */

import { RotateCcw } from "lucide-react";

import type { ConfigLayer } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { layerName } from "@/features/profiles/form";
import { cn } from "@/lib/utils";

/** chipText names a layer on a chip: where a value falls through from. */
const chipText: Record<ConfigLayer, string> = {
  request: "from the message",
  session: "from the session",
  profile: "from the profile",
  model: "from the model",
  default: "default",
};

export type LayerChipProps = {
  /** set says the layer being edited sets the value itself. */
  set: boolean;
  /** from is the layer an unset value falls through from. */
  from: ConfigLayer;
  className?: string;
};

/**
 * LayerChip says where a field's value comes from: set here, in the accent,
 * or the layer it falls through from, muted.
 */
export function LayerChip({ set, from, className }: LayerChipProps) {
  return (
    <span
      className={cn(
        "inline-flex h-4 shrink-0 items-center rounded-full border px-1.5 text-2xs font-medium whitespace-nowrap",
        set
          ? "border-primary/40 bg-primary/10 text-primary"
          : "bg-muted text-muted-foreground border-transparent",
        className,
      )}
    >
      {set ? "set here" : chipText[from]}
    </span>
  );
}

export type FieldHeaderProps = {
  /** htmlFor names the control the label is for; absent, the label names a group by id. */
  htmlFor?: string;
  id?: string;
  label: string;
  set: boolean;
  from: ConfigLayer;
  /** actions sit at the end of the row: a reset, an override, a count. */
  children?: React.ReactNode;
};

/** FieldHeader is a field's first row: its label, where its value comes from, and its actions. */
export function FieldHeader({ htmlFor, id, label, set, from, children }: FieldHeaderProps) {
  return (
    <div className="flex min-h-6 flex-wrap items-center gap-x-2 gap-y-1">
      {htmlFor === undefined ? (
        <span id={id} className="text-sm font-medium">
          {label}
        </span>
      ) : (
        <Label htmlFor={htmlFor} id={id}>
          {label}
        </Label>
      )}
      <LayerChip set={set} from={from} />
      <div className="ml-auto flex items-center gap-1">{children}</div>
    </div>
  );
}

/** ResetButton unsets a field, so that it falls through again. */
export function ResetButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button type="button" size="xs" variant="ghost" onClick={onClick}>
      <RotateCcw aria-hidden />
      Reset<span className="sr-only"> {label}</span>
    </Button>
  );
}

/** Inherited says, muted, what an unset field falls through to. */
export function Inherited({ from, children }: { from: ConfigLayer; children?: React.ReactNode }) {
  return (
    <p className="text-muted-foreground text-xs">
      Not set here: {children ?? "falls through"} from {layerName(from)}.
    </p>
  );
}

export type ChoiceOption<T extends string> = { value: T; label: string };

export type ChoiceGroupProps<T extends string> = {
  /** labelledBy names the group by its header's id. */
  labelledBy: string;
  options: readonly ChoiceOption<T>[];
  /** value is the option set here, or null when the field falls through. */
  value: T | null;
  /** inherited is the option an unset field falls through to, outlined dashed while unset. */
  inherited?: T | undefined;
  onChange: (value: T | null) => void;
};

/**
 * ChoiceGroup is a segmented control over a few values, any of which can be
 * left unset: clicking the chosen one again unsets it. While unset, the
 * value it falls through to is outlined, so the control shows what applies.
 */
export function ChoiceGroup<T extends string>({
  labelledBy,
  options,
  value,
  inherited,
  onChange,
}: ChoiceGroupProps<T>) {
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      spacing={0}
      aria-labelledby={labelledBy}
      value={value ?? ""}
      onValueChange={(next) => {
        const option = options.find((o) => o.value === next);
        onChange(option === undefined ? null : option.value);
      }}
      className="flex-wrap"
    >
      {options.map((o) => (
        <ToggleGroupItem
          key={o.value}
          value={o.value}
          className={cn(
            "h-6 px-2.5 text-xs font-normal",
            "data-[state=on]:border-primary/40 data-[state=on]:bg-primary/10 data-[state=on]:text-primary data-[state=on]:font-medium",
            value === null &&
              inherited === o.value &&
              "text-foreground border-dashed border-muted-foreground/60",
          )}
        >
          {o.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}
