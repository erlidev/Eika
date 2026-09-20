/**
 * The strip above the composer: what the run is doing, which model it uses and
 * how hard it is thinking, how full its context window is, how fast it is
 * decoding, and the way to abort.
 */

import { Brain, Loader2, Radio, Square, WifiOff } from "lucide-react";

import type { StreamStatus } from "@/api/stream";
import { useStreamStatus } from "@/api/useStream";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ContextMeter, DecodeRate } from "@/features/session/ContextMeter";
import { useAbortRun, useRunStatus } from "@/features/session/queries";
import { useSessionStore } from "@/features/session/store";
import { useModel, useModels, useProviders, useUpdateModel } from "@/features/providers";
import { nextEffort } from "@/features/providers/efforts";
import type { Model } from "@/api/types";
import { cn } from "@/lib/utils";

export type RunStatusBarProps = {
  sessionId: string;
  /** model is the model the composer will use. */
  model: string;
  /** onModelChange overrides the model for this session only. */
  onModelChange: (model: string) => void;
};

export function RunStatusBar({ sessionId, model, onModelChange }: RunStatusBarProps) {
  const status = useRunStatus(sessionId);
  const abort = useAbortRun(sessionId);
  const models = useModels();
  const providers = useProviders();
  const stream = useStreamStatus();
  const meter = useSessionStore((s) => s.meter);
  const dropped = useSessionStore((s) => s.dropped);
  const run = status.data?.run;
  const active = status.data?.active ?? false;
  const chosen = useModel(model);

  return (
    <div className="text-muted-foreground flex flex-wrap items-center gap-2 border-t px-3 py-1.5 text-xs">
      {active ? (
        <span className="text-foreground flex items-center gap-1.5 font-medium">
          <Loader2 aria-hidden className="size-3.5 animate-spin" />
          Running
        </span>
      ) : (
        <Badge variant="secondary" className="font-mono text-2xs">
          {run?.state ?? "idle"}
        </Badge>
      )}
      {run?.error !== undefined && run.error !== "" && (
        <span className="text-destructive truncate">{run.error}</span>
      )}

      <Select value={model} onValueChange={onModelChange}>
        <SelectTrigger size="sm" className="h-6 w-auto gap-1 text-xs" aria-label="Model">
          <SelectValue placeholder="default model" />
        </SelectTrigger>
        <SelectContent>
          {(providers.data?.providers ?? []).map((provider) => {
            const own = (models.data?.models ?? []).filter((m) => m.provider_id === provider.id);
            if (own.length === 0) return null;
            return (
              <SelectGroup key={provider.id}>
                <SelectLabel className="text-xs">{provider.name}</SelectLabel>
                {own.map((entry) => (
                  <SelectItem key={entry.id} value={entry.name} className="text-xs">
                    {entry.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            );
          })}
        </SelectContent>
      </Select>

      {chosen && <EffortCycle model={chosen} />}

      {meter && <ContextMeter meter={meter} />}
      {meter && <DecodeRate meter={meter} />}

      <span className="ml-auto flex items-center gap-2">
        {dropped > 0 && <span title="Events the connection lost">{dropped} dropped</span>}
        <LiveUpdates status={stream} />
        {active && run && (
          <Button
            size="xs"
            variant="destructive"
            disabled={abort.isPending}
            onClick={() => {
              abort.mutate(run.id);
            }}
          >
            <Square aria-hidden className="size-3" />
            Abort
          </Button>
        )}
      </span>
    </div>
  );
}

/** liveText is what each state of the event stream means for the page. */
const liveText: Record<StreamStatus, { label: string; title: string }> = {
  open: {
    label: "Live",
    title:
      "Live updates are on: runs, output, and changes from other browsers appear as they happen.",
  },
  connecting: {
    label: "Connecting…",
    title: "Connecting to the harness for live updates.",
  },
  reconnecting: {
    label: "Reconnecting…",
    title:
      "The live connection to the harness dropped and is being retried. Output from a run appears once it is back.",
  },
  idle: {
    label: "Not live",
    title: "No live connection to the harness: this page shows what it last loaded.",
  },
};

/**
 * LiveUpdates shows whether the event stream is connected, which is what
 * keeps a run's output and the rest of the page current.
 */
function LiveUpdates({ status }: { status: StreamStatus }) {
  const { label, title } = liveText[status];
  const Icon = status === "open" ? Radio : status === "idle" ? WifiOff : Loader2;
  return (
    <span
      role="status"
      title={title}
      aria-label={`Live updates: ${label}`}
      className={cn(
        "flex items-center gap-1",
        status === "open" && "text-success",
        status === "reconnecting" && "text-warning",
      )}
    >
      <Icon
        aria-hidden
        className={cn(
          "size-3.5",
          (status === "connecting" || status === "reconnecting") && "animate-spin",
        )}
      />
      {label}
    </span>
  );
}

/**
 * EffortCycle is how hard the model thinks, and the way to change it without
 * leaving the session: one click moves to the next word in the model's own
 * list. The list is the model's setting, so the change applies to the next
 * run in any session that uses it, which is why the button says so.
 */
function EffortCycle({ model }: { model: Model }) {
  const update = useUpdateModel();
  const efforts = model.reasoning_efforts;
  if (efforts.length === 0) return null;
  const current = model.reasoning_effort ?? "";
  const next = nextEffort(current, efforts);

  return (
    <button
      type="button"
      disabled={update.isPending}
      title={
        `Reasoning effort for ${model.name}: ${current === "" ? "the endpoint's default" : current}. ` +
        `Click for ${next}. It applies to this model's next run, wherever it runs.`
      }
      className={cn(
        "hover:bg-accent flex h-6 items-center gap-1 rounded-md border px-1.5 transition-colors",
        "focus-visible:ring-ring focus-visible:ring-1 focus-visible:outline-none",
        "disabled:opacity-50",
      )}
      onClick={() => {
        update.mutate({ id: model.id, input: { reasoning_effort: next } });
      }}
    >
      <Brain aria-hidden className="size-3" />
      <span className="sr-only">Reasoning effort, click to cycle: </span>
      <span className="font-mono">{current === "" ? "default" : current}</span>
    </button>
  );
}
