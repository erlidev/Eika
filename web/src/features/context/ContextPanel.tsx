/**
 * The Context panel: what a model request sends, at a glance. The next
 * request is previewed live with the same assembly a run uses; any recorded
 * call can be chosen instead. The panel says how full the context window is
 * and what fills it, part by part, with the key parameters; each part, and
 * the Inspect button, opens the Context inspector, which lays the request
 * open whole.
 */

import { Maximize2 } from "lucide-react";
import { useState } from "react";

import type { ModelContext } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { ContextInspector, RequestSelect } from "@/features/context/ContextInspector";
import { Dot, PartsBar, WindowMeter } from "@/features/context/parts";
import { nextRequest, useRequestSelection } from "@/features/context/queries";
import { estimatedTotal, percent, scale, segments, viewOf } from "@/features/context/segments";
import type { View } from "@/features/context/segments";
import { useSessionConfiguration } from "@/features/profiles";
import { useSessionStore } from "@/features/session";
import { formatTokens } from "@/lib/format";

export type ContextPanelProps = {
  sessionId: string;
  /** chat says the session has no workspace, so its base prompt is a chat's. */
  chat: boolean;
};

export function ContextPanel({ sessionId, chat }: ContextPanelProps) {
  const model = useSessionStore((s) => s.model);
  const requests = useRequestSelection(sessionId, model);
  const config = useSessionConfiguration(sessionId);
  const [inspecting, setInspecting] = useState(false);
  const [view, setView] = useState<View>("system");
  const { shown } = requests;
  const inspect = (to: View) => {
    setView(to);
    setInspecting(true);
  };

  return (
    <div className="space-y-3 p-3 text-xs">
      <div className="flex items-end gap-2">
        <div className="min-w-0 flex-1 space-y-1">
          <Label htmlFor={`context-request-${sessionId}`} className="text-xs">
            Request
          </Label>
          <RequestSelect
            id={`context-request-${sessionId}`}
            requests={requests}
            className="w-full"
          />
        </div>
      </div>
      {shown.isPending && <Notice tone="pending">Assembling the request…</Notice>}
      {shown.isError && (
        <LoadError
          what={requests.selected === nextRequest ? "the next request" : "the recorded request"}
          error={shown.error}
          retrying={shown.isFetching}
          retry={() => void shown.refetch()}
        />
      )}
      {shown.data !== undefined && <Summary context={shown.data} onInspect={inspect} />}
      <ContextInspector
        sessionId={sessionId}
        chat={chat}
        open={inspecting}
        onOpenChange={setInspecting}
        view={view}
        onView={setView}
        requests={requests}
        config={config.data}
      />
    </div>
  );
}

type SummaryProps = {
  context: ModelContext;
  onInspect: (view: View) => void;
};

/** Summary is the request at a glance: its size, the window, its parts, and its key parameters. */
function Summary({ context, onInspect }: SummaryProps) {
  const parts = segments(context);
  const total = estimatedTotal(parts);
  const ratio = scale(context);
  const measured = context.request?.input_tokens ?? 0;
  const p = context.parameters;
  const keys: [string, string][] = [
    ["model", p.model === "" ? "none" : p.model],
    ...(p.sampling.reasoning_effort === undefined
      ? []
      : [["effort", p.sampling.reasoning_effort] as [string, string]]),
    ...(p.sampling.temperature === undefined
      ? []
      : [["temperature", String(p.sampling.temperature)] as [string, string]]),
    ["tools", String((context.tools ?? []).length)],
  ];

  return (
    <div className="space-y-3">
      <div className="space-y-0.5">
        <p className="flex flex-wrap items-baseline gap-x-1.5">
          <span className="font-mono text-lg font-semibold tabular-nums">
            ~{formatTokens(total)}
          </span>
          <span className="text-muted-foreground">tokens estimated</span>
        </p>
        <p className="text-muted-foreground">
          {ratio !== undefined && context.request === undefined && (
            <>
              <span className="font-mono tabular-nums">~{formatTokens(total * ratio)}</span> scaled
              to the last measured call.{" "}
            </>
          )}
          {measured > 0 && (
            <>
              <span className="font-mono tabular-nums">{measured.toLocaleString("en-US")}</span>{" "}
              measured by the endpoint.{" "}
            </>
          )}
          {context.request === undefined
            ? "What a message sent now is added to."
            : `Sent to ${context.request.model}.`}
        </p>
      </div>

      <WindowMeter context={context} total={total} />

      <div className="space-y-1.5">
        <PartsBar
          parts={parts}
          total={total}
          onOpen={(kind) => {
            onInspect(viewOf(kind));
          }}
        />
        <ul aria-label="Parts" className="-mx-1">
          {parts.map((part) => (
            <li key={part.kind}>
              <button
                type="button"
                onClick={() => {
                  onInspect(viewOf(part.kind));
                }}
                className="hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-2 rounded-md px-1 py-0.5 text-left transition-colors focus-visible:ring-1 focus-visible:outline-none"
              >
                <Dot kind={part.kind} />
                <span className="min-w-0 flex-1 truncate">{part.label}</span>
                <span className="text-muted-foreground w-10 text-right font-mono tabular-nums">
                  {percent(part.tokens, total)}
                </span>
                <span className="w-12 text-right font-mono tabular-nums">
                  {formatTokens(part.tokens)}
                </span>
              </button>
            </li>
          ))}
        </ul>
      </div>

      {context.context_files_unread !== undefined && context.context_files_unread !== "" && (
        <Notice>
          The workspace is not running, so its context files are not read here. A run starts it and
          reads them.
        </Notice>
      )}
      {context.dropped_effort !== undefined && context.dropped_effort !== "" && (
        <Notice>
          The reasoning effort <span className="font-mono">{context.dropped_effort}</span> is not
          one the model offers, so it is not sent.
        </Notice>
      )}

      <dl className="bg-muted/40 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 rounded-md border px-2 py-1.5">
        {keys.map(([name, value]) => (
          <div key={name} className="contents">
            <dt className="text-muted-foreground">{name}</dt>
            <dd className="truncate font-mono">{value}</dd>
          </div>
        ))}
      </dl>

      <Button
        type="button"
        variant="outline"
        size="sm"
        className="w-full"
        onClick={() => {
          onInspect("system");
        }}
      >
        <Maximize2 aria-hidden />
        Inspect the whole request
      </Button>
    </div>
  );
}
