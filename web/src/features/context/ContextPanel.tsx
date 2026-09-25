/**
 * The Context panel: what a model request sends, part by part. The next
 * request is previewed live with the same assembly a run uses; any recorded
 * call can be opened instead. A stacked bar shows how each part fills the
 * context, and each segment opens its part: the system prompt's sections,
 * the tool schemas, the messages, and the parameters with the layer each
 * came from.
 */

import { ChevronRight } from "lucide-react";
import { useState } from "react";

import type { ConfigLayer, Message, ModelContext, ModelRequest, ToolSchema } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { OutputBlock } from "@/components/OutputBlock";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import {
  useNextRequest,
  useRecordedRequest,
  useRecordedRequests,
} from "@/features/context/queries";
import { estimatedTotal, scale, segmentLabels, segments, share } from "@/features/context/segments";
import type { Segment, SegmentKind } from "@/features/context/segments";
import { useSessionStore } from "@/features/session";
import { formatAgo, formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

export type ContextPanelProps = {
  sessionId: string;
};

/** next is the request select's value for the request a run started now would send. */
const next = "next";

export function ContextPanel({ sessionId }: ContextPanelProps) {
  const model = useSessionStore((s) => s.model);
  const [selected, setSelected] = useState(next);
  const records = useRecordedRequests(sessionId);
  const preview = useNextRequest(sessionId, model);
  const recorded = useRecordedRequest(sessionId, selected === next ? "" : selected);
  const shown = selected === next ? preview : recorded;
  const list = records.data ?? [];

  return (
    <div className="space-y-3 p-3 text-xs">
      <div className="space-y-1">
        <Label htmlFor={`context-request-${sessionId}`} className="text-xs">
          Request
        </Label>
        <Select value={selected} onValueChange={setSelected}>
          <SelectTrigger id={`context-request-${sessionId}`} size="sm" className="w-full text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={next} className="text-xs">
              Next request, live
            </SelectItem>
            {[...list].reverse().map((r, i) => (
              <SelectItem key={r.id} value={r.id} className="text-xs">
                {requestLabel(r, list.length - i)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {records.isError && (
          <LoadError
            what="the recorded requests"
            error={records.error}
            retrying={records.isFetching}
            retry={() => void records.refetch()}
          />
        )}
      </div>
      {shown.isPending && <Notice tone="pending">Assembling the request…</Notice>}
      {shown.isError && (
        <LoadError
          what={selected === next ? "the next request" : "the recorded request"}
          error={shown.error}
          retrying={shown.isFetching}
          retry={() => void shown.refetch()}
        />
      )}
      {shown.data && <RequestView key={selected} context={shown.data} />}
    </div>
  );
}

/** requestLabel names a recorded call in the select: its number, model, and measured input. */
function requestLabel(r: ModelRequest, n: number): string {
  const measured = r.input_tokens > 0 ? ` · ${formatTokens(r.input_tokens)} in` : "";
  return `#${String(n)} · ${r.model}${measured} · ${formatAgo(r.created_at)}`;
}

/** segmentColors are the bar's colours, from the theme's chart tokens. */
const segmentColors: Record<SegmentKind, string> = {
  base: "bg-chart-1",
  context_files: "bg-chart-2",
  instructions: "bg-chart-3",
  builtin_tools: "bg-chart-5",
  mcp_tools: "bg-chart-4",
  messages: "bg-primary/40",
};

/** OpenPart names what the panel shows open: a part of the request, or the parameters. */
type OpenPart = SegmentKind | "parameters";

function RequestView({ context }: { context: ModelContext }) {
  const parts = segments(context);
  const total = estimatedTotal(parts);
  const ratio = scale(context);
  const [open, setOpen] = useState<ReadonlySet<OpenPart>>(new Set());
  const [raw, setRaw] = useState(false);
  const toggle = (part: OpenPart, on: boolean) => {
    const nextOpen = new Set(open);
    if (on) nextOpen.add(part);
    else nextOpen.delete(part);
    setOpen(nextOpen);
  };
  const reveal = (part: SegmentKind) => {
    toggle(part, true);
    document.getElementById(`context-part-${part}`)?.scrollIntoView({ block: "nearest" });
  };

  return (
    <div className="space-y-3">
      <Summary context={context} total={total} ratio={ratio} />
      {total > 0 && (
        <div
          className="bg-muted flex h-3 overflow-hidden rounded-md"
          role="group"
          aria-label="Tokens by part"
        >
          {parts.map((part) => (
            <button
              key={part.kind}
              type="button"
              title={`${part.label}: ~${formatTokens(part.tokens)} tokens`}
              aria-label={`${part.label}, about ${formatTokens(part.tokens)} tokens`}
              className={cn(
                "h-full min-w-1 transition-opacity hover:opacity-75 focus-visible:opacity-75 focus-visible:outline-none",
                segmentColors[part.kind],
              )}
              style={{ width: `${String(share(part.tokens, total))}%` }}
              onClick={() => {
                reveal(part.kind);
              }}
            />
          ))}
        </div>
      )}
      <ul className="grid grid-cols-2 gap-x-3 gap-y-0.5" aria-label="Legend">
        {parts.map((part) => (
          <li key={part.kind} className="flex min-w-0 items-center gap-1.5">
            <span
              aria-hidden
              className={cn("size-2 shrink-0 rounded-full", segmentColors[part.kind])}
            />
            <span className="truncate">{part.label}</span>
            <span className="text-muted-foreground ml-auto font-mono tabular-nums">
              {formatTokens(part.tokens)}
            </span>
          </li>
        ))}
      </ul>
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

      <div className="flex items-center justify-end gap-2">
        <Label htmlFor="context-raw" className="text-muted-foreground text-xs font-normal">
          Raw schemas
        </Label>
        <Switch id="context-raw" size="sm" checked={raw} onCheckedChange={setRaw} />
      </div>
      <div className="divide-y overflow-hidden rounded-md border">
        {parts.map((part) => (
          <Part
            key={part.kind}
            part={part}
            open={open.has(part.kind)}
            onOpenChange={(on) => {
              toggle(part.kind, on);
            }}
          >
            <PartBody kind={part.kind} context={context} raw={raw} />
          </Part>
        ))}
        <Part
          part={{ kind: "messages", label: "Parameters", tokens: 0 }}
          id="context-part-parameters"
          open={open.has("parameters")}
          onOpenChange={(on) => {
            toggle("parameters", on);
          }}
        >
          <Parameters context={context} />
        </Part>
      </div>
    </div>
  );
}

type SummaryProps = { context: ModelContext; total: number; ratio: number | undefined };

/** Summary says how large the request is, and how that number was reached. */
function Summary({ context, total, ratio }: SummaryProps) {
  const measured = context.request?.input_tokens ?? 0;
  return (
    <div className="space-y-0.5">
      <p>
        <span className="font-mono tabular-nums">~{formatTokens(total)}</span> tokens estimated
        {ratio !== undefined && context.request === undefined && (
          <>
            , <span className="font-mono tabular-nums">~{formatTokens(total * ratio)}</span> scaled
            to the last measured call
          </>
        )}
        {measured > 0 && (
          <>
            ; <span className="font-mono tabular-nums">{measured.toLocaleString("en-US")}</span>{" "}
            measured by the endpoint
          </>
        )}
      </p>
      <p className="text-muted-foreground">
        Estimates count four bytes to a token.
        {context.request === undefined
          ? " This is what a message sent now would be added to."
          : ` Sent to ${context.request.model}.`}
      </p>
    </div>
  );
}

type PartProps = {
  part: Segment;
  id?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: React.ReactNode;
};

function Part({ part, id, open, onOpenChange, children }: PartProps) {
  return (
    <Collapsible open={open} onOpenChange={onOpenChange} id={id ?? `context-part-${part.kind}`}>
      <CollapsibleTrigger className="group hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs font-medium transition-colors focus-visible:ring-1 focus-visible:outline-none focus-visible:ring-inset">
        <ChevronRight
          aria-hidden
          className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-90"
        />
        {part.label}
        {part.tokens > 0 && (
          <span className="text-muted-foreground ml-auto font-mono font-normal tabular-nums">
            ~{formatTokens(part.tokens)}
          </span>
        )}
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-2 border-t px-2 py-2">{children}</CollapsibleContent>
    </Collapsible>
  );
}

function PartBody({
  kind,
  context,
  raw,
}: {
  kind: SegmentKind;
  context: ModelContext;
  raw: boolean;
}) {
  switch (kind) {
    case "builtin_tools":
    case "mcp_tools":
      return (
        <Tools
          tools={(context.tools ?? []).filter(
            (t) => (t.source === "mcp") === (kind === "mcp_tools"),
          )}
          raw={raw}
        />
      );
    case "messages":
      return <Messages messages={context.messages ?? []} />;
    default: {
      const section = (context.sections ?? []).find((s) => s.kind === kind);
      if (section === undefined) return null;
      if (section.files !== undefined && section.files.length > 0) {
        return (
          <>
            {section.files.map((f) => (
              <div key={f.path} className="space-y-1">
                <p className="flex gap-2 font-mono">
                  {f.path}
                  <span className="text-muted-foreground ml-auto">~{formatTokens(f.tokens)}</span>
                </p>
                <OutputBlock
                  label={f.path}
                  maxHeightClass="max-h-60"
                  className="whitespace-pre-wrap"
                >
                  {f.text}
                </OutputBlock>
              </div>
            ))}
          </>
        );
      }
      return (
        <OutputBlock
          label={segmentLabels[kind]}
          maxHeightClass="max-h-60"
          className="whitespace-pre-wrap"
        >
          {section.text}
        </OutputBlock>
      );
    }
  }
}

function Tools({ tools, raw }: { tools: ToolSchema[]; raw: boolean }) {
  return (
    <ul className="space-y-2">
      {tools.map((t) => (
        <li key={t.name} className="space-y-1">
          <p className="flex gap-2">
            <span className="font-mono font-medium break-all">{t.name}</span>
            <span className="text-muted-foreground ml-auto shrink-0 font-mono">
              ~{formatTokens(t.tokens)}
            </span>
          </p>
          <p className="text-muted-foreground whitespace-pre-wrap">{t.description}</p>
          <OutputBlock label={`Schema of ${t.name}`} maxHeightClass="max-h-48">
            {JSON.stringify(t.schema, null, raw ? undefined : 2)}
          </OutputBlock>
        </li>
      ))}
    </ul>
  );
}

function Messages({ messages }: { messages: Message[] }) {
  if (messages.length === 0) {
    return (
      <p className="text-muted-foreground">
        No messages yet: the next one starts the conversation.
      </p>
    );
  }
  return (
    <ol className="space-y-1.5">
      {messages.map((m, i) => (
        // The messages are a fixed list for this request; their order is their identity.
        <li key={`${String(i)}:${m.role}`} className="space-y-0.5">
          <p className="text-muted-foreground font-mono text-2xs uppercase">{m.role}</p>
          <p className="line-clamp-3 break-words whitespace-pre-wrap">
            {m.content !== undefined && m.content !== ""
              ? m.content
              : (m.tool_calls ?? [])
                  .map((c) => `${c.name}(${JSON.stringify(c.arguments)})`)
                  .join("\n")}
          </p>
        </li>
      ))}
    </ol>
  );
}

/** layerText is how the parameters table names where a value came from. */
const layerText: Record<ConfigLayer, string> = {
  request: "message",
  session: "session",
  profile: "profile",
  model: "model",
  default: "default",
};

function Parameters({ context }: { context: ModelContext }) {
  const p = context.parameters;
  const rows: { name: string; value: string; key: string }[] = [
    { name: "model", value: p.model, key: "model" },
    ...Object.entries(p.sampling).map(([name, value]) => ({
      name,
      value: Array.isArray(value) ? value.map((v) => JSON.stringify(v)).join(", ") : String(value),
      key: `sampling.${name}`,
    })),
    ...(p.thinking_switch === undefined
      ? []
      : [{ name: "thinking_switch", value: p.thinking_switch, key: "thinking_switch" }]),
    { name: "preserve_thinking", value: String(p.preserve_thinking), key: "preserve_thinking" },
  ];
  return (
    <table className="w-full">
      <thead className="sr-only">
        <tr>
          <th>Parameter</th>
          <th>Value</th>
          <th>From</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => {
          const layer = context.sources?.[r.key];
          return (
            <tr key={r.name}>
              <td className="py-0.5 pr-2 font-mono">{r.name}</td>
              <td className="py-0.5 pr-2 font-mono break-all">{r.value === "" ? "—" : r.value}</td>
              <td className="text-muted-foreground py-0.5 text-right">
                {layer === undefined ? "" : layerText[layer]}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
