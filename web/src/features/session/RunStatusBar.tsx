/**
 * The strip under the composer: what the run is doing, which model it uses and
 * how hard it is thinking, how full its context window is, and the way to
 * abort. It sits below the box because the box is what the eye goes to, and a
 * status line is read after it, not through it.
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
import { SessionProfile, useSessionConfiguration } from "@/features/profiles";
import { ContextMeter } from "@/features/session/ContextMeter";
import { useAbortRun, useRunStatus } from "@/features/session/queries";
import { useSessionStore } from "@/features/session/store";
import { useModel, useModels, useProviders, useUpdateModel } from "@/features/providers";
import { nextEffort } from "@/features/providers/efforts";
import type { Model } from "@/api/types";
import { cn } from "@/lib/utils";

export type RunStatusBarProps = {
  sessionId: string;
  /** chat says the session has no workspace. */
  chat: boolean;
  /** overridden says the session sets something of its own over its profile. */
  overridden: boolean;
  /** model is the model the composer will use. */
  model: string;
  /** onModelChange overrides the model for this session only. */
  onModelChange: (model: string) => void;
};

export function RunStatusBar({
  sessionId,
  chat,
  overridden,
  model,
  onModelChange,
}: RunStatusBarProps) {
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
  // A profile or the session can set the effort over the model's own, and
  // then the model's cycle would change nothing the next run sends.
  const configuration = useSessionConfiguration(sessionId);
  const resolved = configuration.data?.resolved;
  const effortSource =
    resolved?.model === model ? resolved.sources?.["sampling.reasoning_effort"] : undefined;
  const configuredEffort = resolved?.sampling.reasoning_effort ?? "";

  return (
    <div className="text-muted-foreground flex flex-wrap items-center gap-2 border-t px-3 py-1 text-xs">
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

      <SessionProfile sessionId={sessionId} chat={chat} overridden={overridden} />

      {chosen &&
        (effortSource === "session" || effortSource === "profile" ? (
          <span
            className="flex h-6 items-center gap-1 rounded-md border border-dashed px-1.5"
            title={`Reasoning effort ${configuredEffort === "" ? "default" : configuredEffort}, set by the ${effortSource === "session" ? "session" : "profile"}. Change it in the profile's settings.`}
          >
            <Brain aria-hidden className="size-3" />
            <span className="sr-only">Reasoning effort, set by the {effortSource}: </span>
            <span className="font-mono">
              {configuredEffort === "" ? "default" : configuredEffort}
            </span>
          </span>
        ) : (
          <EffortCycle model={chosen} />
        ))}

      {meter && <ContextMeter meter={meter} />}

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
  open: { label: "Live", title: "Live updates on" },
  connecting: { label: "Connecting…", title: "Connecting for live updates" },
  reconnecting: { label: "Reconnecting…", title: "Live connection dropped, retrying" },
  idle: { label: "Not live", title: "No live connection: showing the last load" },
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
 * run in any session that uses it.
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
      title={`Reasoning effort: ${current === "" ? "default" : current}. Click for ${next}.`}
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
