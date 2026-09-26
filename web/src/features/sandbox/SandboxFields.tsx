/**
 * The fields of a sandbox: its resource limits, its network, and the ports
 * the harness forwards to it. The settings' defaults, a new workspace, and
 * the Sandbox panel all edit a sandbox with them. They hold a SandboxDraft
 * and report changes; checking and saving belong to whoever holds the draft.
 */

import { Plus, X } from "lucide-react";
import { Slider } from "radix-ui";
import { useState } from "react";

import type { SandboxHost } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  bounds,
  egressModes,
  formatCores,
  formatMemory,
  normalizePattern,
} from "@/features/sandbox/form";
import type { SandboxDraft, SandboxProblems } from "@/features/sandbox/form";
import { cn } from "@/lib/utils";

export type SandboxFieldsProps = {
  /** id prefixes every control's id, so two editors can share a page. */
  id: string;
  draft: SandboxDraft;
  onChange: (change: Partial<SandboxDraft>) => void;
  /** host is what the Docker host has; undefined while it loads or when Docker is down. */
  host: SandboxHost | undefined;
  problems: SandboxProblems;
};

/** LimitsFields edits a sandbox's CPU, memory, and process limits. */
export function LimitsFields({ id, draft, onChange, host, problems }: SandboxFieldsProps) {
  const cpus = Number(draft.cpus.trim() === "" ? 0 : draft.cpus);
  const memoryMb = Number(draft.memoryMb.trim() === "" ? 0 : draft.memoryMb);
  const hostMemoryMb = host !== undefined ? Math.floor(host.memory_bytes / 2 ** 20) : 0;
  return (
    <div className="space-y-3">
      <LimitField
        id={`${id}-cpus`}
        label="CPU"
        unit="cores"
        value={draft.cpus}
        readout={Number.isFinite(cpus) ? formatCores(cpus) : ""}
        max={host?.cpus ?? 0}
        step={0.25}
        problem={problems.cpus}
        hint="How many cores the sandbox's processes may use between them."
        onChange={(cpusText) => {
          onChange({ cpus: cpusText });
        }}
      />
      <LimitField
        id={`${id}-memory`}
        label="Memory"
        unit="MiB"
        value={draft.memoryMb}
        readout={Number.isFinite(memoryMb) ? formatMemory(memoryMb) : ""}
        max={hostMemoryMb}
        step={256}
        problem={problems.memoryMb}
        hint="What the processes may hold, with no swap beyond it. Past it, the kernel stops the largest process."
        onChange={(memoryText) => {
          onChange({ memoryMb: memoryText });
        }}
      />
      <LimitField
        id={`${id}-pids`}
        label="Processes"
        unit="max"
        value={draft.pids}
        readout=""
        max={0}
        step={1}
        problem={problems.pids}
        hint="Processes and threads together. A few thousand stops a fork bomb and leaves room for a parallel build."
        onChange={(pidsText) => {
          onChange({ pids: pidsText });
        }}
      />
    </div>
  );
}

type LimitFieldProps = {
  id: string;
  label: string;
  unit: string;
  value: string;
  /** readout says the value in words beside the label. */
  readout: string;
  /** max is the slider's end, the host's capacity; zero draws no slider. */
  max: number;
  step: number;
  problem: string | undefined;
  hint: string;
  onChange: (text: string) => void;
};

/**
 * LimitField is one limit: a slider from no limit to the whole host, when
 * the host's capacity is known, beside an input that takes an exact value.
 * An empty input is no limit.
 */
function LimitField({
  id,
  label,
  unit,
  value,
  readout,
  max,
  step,
  problem,
  hint,
  onChange,
}: LimitFieldProps) {
  const hintId = `${id}-hint`;
  const n = Number(value.trim() === "" ? 0 : value);
  const position = Number.isFinite(n) ? Math.min(Math.max(n, 0), max) : 0;
  return (
    <div className="space-y-1">
      <div className="flex items-baseline gap-2">
        <Label htmlFor={id}>{label}</Label>
        {readout !== "" && <span className="text-muted-foreground text-xs">{readout}</span>}
      </div>
      <div className="flex items-center gap-3">
        {max > 0 && (
          <div className="flex-1 space-y-0.5">
            <Slider.Root
              min={0}
              max={max}
              step={step}
              value={[position]}
              className="relative flex w-full touch-none items-center py-1.5 select-none"
              onValueChange={(values) => {
                const next = values[0];
                if (next !== undefined) onChange(next === 0 ? "" : String(next));
              }}
            >
              <Slider.Track className="bg-muted relative h-1 grow overflow-hidden rounded-full">
                <Slider.Range className="bg-primary absolute h-full" />
              </Slider.Track>
              <Slider.Thumb
                aria-label={`${label} limit slider`}
                className="border-primary bg-background ring-ring/50 block size-3 rounded-full border transition-shadow hover:ring-3 focus-visible:ring-3 focus-visible:outline-none"
              />
            </Slider.Root>
            <div
              aria-hidden
              className="text-muted-foreground flex justify-between font-mono text-2xs tabular-nums"
            >
              <span>none</span>
              <span>{max}</span>
            </div>
          </div>
        )}
        <div className={cn("flex items-center gap-1.5", max === 0 && "flex-1")}>
          <Input
            id={id}
            value={value}
            placeholder="none"
            inputMode="decimal"
            autoComplete="off"
            aria-invalid={problem !== undefined}
            aria-describedby={hintId}
            className={cn("h-7 text-right font-mono text-xs", max > 0 ? "w-20" : "w-28")}
            onChange={(e) => {
              onChange(e.target.value);
            }}
          />
          <span className="text-muted-foreground w-9 text-xs">{unit}</span>
        </div>
      </div>
      <p
        id={hintId}
        className={cn(
          "text-xs",
          problem === undefined ? "text-muted-foreground" : "text-destructive",
        )}
      >
        {problem ?? hint}
      </p>
    </div>
  );
}

/** EgressFields edits what a sandbox may reach: the mode, and the allowlist it uses. */
export function EgressFields({ id, draft, onChange, host, problems }: SandboxFieldsProps) {
  const labelId = `${id}-egress-label`;
  const control = host?.egress_control ?? false;
  const mode = egressModes.find((m) => m.value === draft.mode);
  return (
    <div className="space-y-2">
      <div className="space-y-1">
        <span id={labelId} className="text-sm font-medium">
          Network
        </span>
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          spacing={0}
          aria-labelledby={labelId}
          value={draft.mode}
          onValueChange={(next) => {
            const chosen = egressModes.find((m) => m.value === next);
            if (chosen !== undefined) onChange({ mode: chosen.value });
          }}
        >
          {egressModes.map((m) => (
            <ToggleGroupItem
              key={m.value}
              value={m.value}
              disabled={m.value !== "open" && !control && draft.mode !== m.value}
              className="data-[state=on]:border-primary/40 data-[state=on]:bg-primary/10 data-[state=on]:text-primary h-6 px-2.5 text-xs font-normal data-[state=on]:font-medium"
            >
              {m.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <p className="text-muted-foreground text-xs">
          {control || draft.mode !== "open"
            ? mode?.hint
            : "This harness runs outside the compose stack, which has the internal network a restricted sandbox needs, so every sandbox's network is open."}
        </p>
      </div>
      {draft.mode === "allowlist" && (
        <AllowlistField
          id={`${id}-allow`}
          allow={draft.allow}
          problem={problems.allow}
          onChange={(allow) => {
            onChange({ allow });
          }}
        />
      )}
    </div>
  );
}

type AllowlistFieldProps = {
  id: string;
  allow: string[];
  problem: string | undefined;
  onChange: (allow: string[]) => void;
};

/** AllowlistField edits the hosts an allowlist admits, as chips with an input to add one. */
function AllowlistField({ id, allow, problem, onChange }: AllowlistFieldProps) {
  const [typed, setTyped] = useState("");
  const [typedProblem, setTypedProblem] = useState<string | undefined>(undefined);
  const hintId = `${id}-hint`;
  const add = () => {
    if (typed.trim() === "") return;
    const { pattern, problem: bad } = normalizePattern(typed);
    if (pattern === undefined) {
      setTypedProblem(bad);
      return;
    }
    if (!allow.includes(pattern)) onChange([...allow, pattern]);
    setTyped("");
    setTypedProblem(undefined);
  };
  const shown = typedProblem ?? problem;
  return (
    <div className="space-y-1">
      <Label htmlFor={id}>Allowed hosts</Label>
      <div className="border-input focus-within:ring-ring/50 focus-within:border-ring flex min-h-7 flex-wrap items-center gap-1 rounded-md border px-1.5 py-1 focus-within:ring-3">
        {allow.map((host) => (
          <span
            key={host}
            className="bg-muted inline-flex h-5 items-center gap-1 rounded-md border pr-0.5 pl-1.5 font-mono text-xs"
          >
            {host}
            <button
              type="button"
              aria-label={`Remove ${host} from the allowlist`}
              className="hover:bg-accent text-muted-foreground rounded-md p-0.5 transition-colors"
              onClick={() => {
                onChange(allow.filter((h) => h !== host));
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
          aria-invalid={shown !== undefined}
          placeholder={allow.length > 0 ? "Add another" : "Type a host, then Enter"}
          className="placeholder:text-muted-foreground h-5 min-w-32 flex-1 bg-transparent font-mono text-xs outline-none"
          onChange={(e) => {
            setTyped(e.target.value);
            setTypedProblem(undefined);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            } else if (e.key === "Backspace" && typed === "" && allow.length > 0) {
              onChange(allow.slice(0, -1));
            }
          }}
          onBlur={add}
        />
      </div>
      <p
        id={hintId}
        className={cn(
          "text-xs",
          shown === undefined ? "text-muted-foreground" : "text-destructive",
        )}
      >
        {shown ??
          "A host such as github.com, or *.github.com for every name below it. Git, npm, pip, Go, curl, and Node reach them through the harness's proxy."}
      </p>
    </div>
  );
}

/** PortsFields edits the container ports the harness forwards previews to. */
export function PortsFields({ id, draft, onChange, problems }: SandboxFieldsProps) {
  const hintId = `${id}-ports-hint`;
  const set = (i: number, change: Partial<{ port: string; label: string }>) => {
    onChange({ ports: draft.ports.map((p, j) => (j === i ? { ...p, ...change } : p)) });
  };
  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium">Forwarded ports</span>
        <Button
          type="button"
          size="xs"
          variant="ghost"
          className="ml-auto"
          disabled={draft.ports.length >= bounds.maxPorts}
          onClick={() => {
            onChange({ ports: [...draft.ports, { port: "", label: "" }] });
          }}
        >
          <Plus aria-hidden />
          Add port
        </Button>
      </div>
      {draft.ports.length === 0 ? (
        <p className="text-muted-foreground text-xs">
          None. Nothing outside the sandbox reaches it but the harness.
        </p>
      ) : (
        <ul className="space-y-1">
          {draft.ports.map((p, i) => (
            // The index is the key: a row keeps its place while its port is edited.
            <li key={i} className="flex items-center gap-1.5">
              <Input
                aria-label={`Port ${String(i + 1)}`}
                value={p.port}
                inputMode="numeric"
                autoComplete="off"
                placeholder="5173"
                aria-describedby={hintId}
                className="h-7 w-20 font-mono text-xs"
                onChange={(e) => {
                  set(i, { port: e.target.value });
                }}
              />
              <Input
                aria-label={`Label of port ${String(i + 1)}`}
                value={p.label}
                autoComplete="off"
                placeholder="What listens there"
                maxLength={bounds.maxPortLabel}
                className="h-7 flex-1 text-xs"
                onChange={(e) => {
                  set(i, { label: e.target.value });
                }}
              />
              <Button
                type="button"
                size="icon-xs"
                variant="ghost"
                aria-label={`Remove port ${p.port === "" ? String(i + 1) : p.port}`}
                onClick={() => {
                  onChange({ ports: draft.ports.filter((_, j) => j !== i) });
                }}
              >
                <X aria-hidden />
              </Button>
            </li>
          ))}
        </ul>
      )}
      <p
        id={hintId}
        className={cn(
          "text-xs",
          problems.ports === undefined ? "text-muted-foreground" : "text-destructive",
        )}
      >
        {problems.ports ??
          "The harness forwards a listed port to your browser alone, on a preview address of its own. A server there must listen on 0.0.0.0, not localhost."}
      </p>
    </div>
  );
}
