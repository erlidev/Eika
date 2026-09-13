/**
 * The strip above the composer: what the run is doing, which model it uses,
 * what the last turn cost, and the way to abort.
 */

import { Loader2, Square } from "lucide-react";

import { useStreamStatus } from "@/api/useStream";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useAbortRun, useRunStatus } from "@/features/session/queries";
import { useSessionStore } from "@/features/session/store";
import { useModels } from "@/features/settings";
import { formatTokens } from "@/lib/format";

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
  const stream = useStreamStatus();
  const usage = useSessionStore((s) => s.usage);
  const dropped = useSessionStore((s) => s.dropped);
  const run = status.data?.run;
  const active = status.data?.active ?? false;

  return (
    <div className="text-muted-foreground flex flex-wrap items-center gap-2 border-t px-3 py-1.5 text-xs">
      {active ? (
        <span className="text-foreground flex items-center gap-1.5 font-medium">
          <Loader2 aria-hidden className="size-3 animate-spin" />
          Running
        </span>
      ) : (
        <Badge variant="secondary" className="font-mono text-[0.7rem]">
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
          {(models.data?.models ?? []).map((entry) => (
            <SelectItem key={entry.name} value={entry.name} className="text-xs">
              {entry.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      {usage && (
        <span className="font-mono tabular-nums">
          {formatTokens(usage.input_tokens)} in · {formatTokens(usage.output_tokens)} out
        </span>
      )}

      <span className="ml-auto flex items-center gap-2">
        {dropped > 0 && <span title="Events the connection lost">{dropped} dropped</span>}
        <span className="font-mono">{stream}</span>
        {active && run && (
          <Button
            size="sm"
            variant="destructive"
            className="h-6 px-2 text-xs"
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
