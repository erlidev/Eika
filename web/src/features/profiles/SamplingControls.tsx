/**
 * The controls of the sampling parameters. A bounded number has a slider
 * beside its input; the reasoning effort is a choice between the words the
 * model offers; stop sequences are chips. Each shows what it falls through
 * to while unset: the slider's thumb rests there, dimmed, and the input's
 * placeholder names it.
 */

import { X } from "lucide-react";
import { Slider } from "radix-ui";
import { useState } from "react";

import type { Configuration, Model } from "@/api/types";
import { Input } from "@/components/ui/input";
import { ChoiceGroup, FieldHeader, ResetButton } from "@/features/profiles/fields";
import { samplingText, sliderPosition, sliderText, sourceOf } from "@/features/profiles/form";
import type { SamplingField } from "@/features/profiles/form";
import { cn } from "@/lib/utils";

export type SamplingControlProps = {
  id: string;
  field: SamplingField;
  /** value is the control's text; "" is not set here. */
  value: string;
  inherited: Configuration;
  problem: string | undefined;
  onChange: (text: string) => void;
  /** model is the model the layer runs, whose efforts the effort control offers. */
  model?: Model | undefined;
};

/** SamplingControl is the control a parameter's field calls for. */
export function SamplingControl(props: SamplingControlProps) {
  const { field, model } = props;
  if (field.range !== undefined) return <SliderControl {...props} />;
  if (field.kind === "list") return <StopControl {...props} />;
  if (
    field.key === "reasoning_effort" &&
    model !== undefined &&
    model.reasoning_efforts.length > 0
  ) {
    return <EffortControl {...props} efforts={model.reasoning_efforts} />;
  }
  return <NumberControl {...props} />;
}

/** fallback is what an unset parameter falls through to, as a placeholder says it. */
function fallback(config: Configuration, field: SamplingField): string {
  const text = samplingText(config.sampling, field.key);
  if (config.sampling[field.key] === undefined || text === "") return "auto";
  return text;
}

/** Hint is the line under a control: what the parameter does, or what is wrong with it. */
function Hint({ id, hint, problem }: { id: string; hint: string; problem: string | undefined }) {
  return (
    <p
      id={id}
      className={
        problem === undefined ? "text-muted-foreground text-xs" : "text-destructive text-xs"
      }
    >
      {problem ?? hint}
    </p>
  );
}

function Header({ id, field, value, inherited, onChange }: SamplingControlProps) {
  return (
    <FieldHeader
      htmlFor={id}
      label={field.label}
      set={value.trim() !== ""}
      from={sourceOf(inherited, `sampling.${field.key}`)}
    >
      {value !== "" && (
        <ResetButton
          label={field.label.toLowerCase()}
          onClick={() => {
            onChange("");
          }}
        />
      )}
    </FieldHeader>
  );
}

function SliderControl(props: SamplingControlProps) {
  const { id, field, value, inherited, problem, onChange } = props;
  const range = field.range;
  if (range === undefined) return null;
  const own = value.trim() !== "";
  const below = inherited.sampling[field.key];
  const position = sliderPosition(range, value, typeof below === "number" ? below : undefined);
  const hintId = `${id}-hint`;
  return (
    <div className="space-y-1.5">
      <Header {...props} />
      <div className="flex items-center gap-3">
        <div className="flex-1 space-y-0.5">
          <Slider.Root
            min={range.min}
            max={range.max}
            step={range.step}
            value={[position]}
            className={cn(
              "relative flex w-full touch-none items-center py-1.5 select-none",
              !own && "opacity-45",
            )}
            onValueChange={(values) => {
              const next = values[0];
              if (next !== undefined) onChange(sliderText(range, next));
            }}
          >
            <Slider.Track className="bg-muted relative h-1 grow overflow-hidden rounded-full">
              <Slider.Range className="bg-primary absolute h-full" />
            </Slider.Track>
            <Slider.Thumb
              aria-label={`${field.label} slider`}
              className="border-primary bg-background ring-ring/50 block size-3 rounded-full border transition-shadow hover:ring-3 focus-visible:ring-3 focus-visible:outline-none"
            />
          </Slider.Root>
          <div
            aria-hidden
            className="text-muted-foreground flex justify-between font-mono text-2xs tabular-nums"
          >
            <span>{range.min}</span>
            <span>{range.max}</span>
          </div>
        </div>
        <Input
          id={id}
          value={value}
          placeholder={fallback(inherited, field)}
          inputMode="decimal"
          autoComplete="off"
          aria-invalid={problem !== undefined}
          aria-describedby={hintId}
          className="h-7 w-20 text-right font-mono text-xs"
          onChange={(e) => {
            onChange(e.target.value);
          }}
        />
      </div>
      <Hint id={hintId} hint={field.hint} problem={problem} />
    </div>
  );
}

function NumberControl(props: SamplingControlProps) {
  const { id, field, value, inherited, problem, onChange } = props;
  const hintId = `${id}-hint`;
  return (
    <div className="space-y-1.5">
      <Header {...props} />
      <Input
        id={id}
        value={value}
        placeholder={fallback(inherited, field)}
        inputMode={field.kind === "text" ? "text" : "numeric"}
        autoComplete="off"
        aria-invalid={problem !== undefined}
        aria-describedby={hintId}
        className="h-7 font-mono text-xs"
        onChange={(e) => {
          onChange(e.target.value);
        }}
      />
      <Hint id={hintId} hint={field.hint} problem={problem} />
    </div>
  );
}

function EffortControl(props: SamplingControlProps & { efforts: readonly string[] }) {
  const { id, field, value, inherited, problem, onChange, efforts } = props;
  const below = inherited.sampling.reasoning_effort;
  const labelId = `${id}-label`;
  return (
    <div className="space-y-1.5">
      <FieldHeader
        id={labelId}
        label={field.label}
        set={value !== ""}
        from={sourceOf(inherited, "sampling.reasoning_effort")}
      >
        {value !== "" && (
          <ResetButton
            label="the reasoning effort"
            onClick={() => {
              onChange("");
            }}
          />
        )}
      </FieldHeader>
      <ChoiceGroup
        labelledBy={labelId}
        options={efforts.map((e) => ({ value: e, label: e }))}
        value={value === "" ? null : value}
        inherited={below}
        onChange={(next) => {
          onChange(next ?? "");
        }}
      />
      <Hint id={`${id}-hint`} hint={field.hint} problem={problem} />
    </div>
  );
}

function StopControl(props: SamplingControlProps) {
  const { id, field, value, inherited, problem, onChange } = props;
  const [typed, setTyped] = useState("");
  const stops = value.split("\n").filter((line) => line !== "");
  const below = inherited.sampling.stop;
  const hintId = `${id}-hint`;
  const add = () => {
    const text = typed;
    if (text === "" || stops.includes(text)) return;
    onChange([...stops, text].join("\n"));
    setTyped("");
  };
  return (
    <div className="space-y-1.5">
      <Header {...props} />
      <div className="border-input focus-within:ring-ring/50 focus-within:border-ring flex min-h-7 flex-wrap items-center gap-1 rounded-md border px-1.5 py-1 focus-within:ring-3">
        {stops.map((stop) => (
          <span
            key={stop}
            className="bg-muted inline-flex h-5 items-center gap-1 rounded-md border pr-0.5 pl-1.5 font-mono text-xs"
          >
            {stop}
            <button
              type="button"
              aria-label={`Remove the stop sequence ${stop}`}
              className="hover:bg-accent text-muted-foreground rounded-md p-0.5 transition-colors"
              onClick={() => {
                onChange(stops.filter((s) => s !== stop).join("\n"));
              }}
            >
              <X aria-hidden className="size-3" />
            </button>
          </span>
        ))}
        <input
          id={id}
          value={typed}
          autoComplete="off"
          aria-describedby={hintId}
          aria-invalid={problem !== undefined}
          placeholder={
            stops.length > 0
              ? "Add another"
              : below !== undefined && below.length > 0
                ? `${below.join(", ")}; type to replace`
                : "Type a sequence, then Enter"
          }
          className="placeholder:text-muted-foreground h-5 min-w-32 flex-1 bg-transparent font-mono text-xs outline-none"
          onChange={(e) => {
            setTyped(e.target.value);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            } else if (e.key === "Backspace" && typed === "" && stops.length > 0) {
              onChange(stops.slice(0, -1).join("\n"));
            }
          }}
          onBlur={add}
        />
      </div>
      <Hint id={hintId} hint={field.hint} problem={problem} />
    </div>
  );
}
