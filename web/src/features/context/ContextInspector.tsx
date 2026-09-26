/**
 * The Context inspector: one model request laid open in a large dialog. A
 * column lists the request's parts with what each costs; the pane beside it
 * shows the chosen part whole: the system prompt as the model reads it,
 * section by section, each tool with its parameters, every message, and
 * the parameters with the layer each came from. A part of the next request
 * links to the setting behind it, and the whole request copies as JSON.
 */

import { Check, Copy } from "lucide-react";
import { useState } from "react";

import type {
  ConfigLayer,
  Message,
  ModelContext,
  ModelRequest,
  SessionConfiguration,
  ToolSchema,
} from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { OutputBlock } from "@/components/OutputBlock";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Dot, EditLink, PartsBar, WindowMeter } from "@/features/context/parts";
import { nextRequest } from "@/features/context/queries";
import type { RequestSelection } from "@/features/context/queries";
import {
  editTargetOf,
  percent,
  segmentColors,
  viewOf,
  estimatedTotal,
  parameterTarget,
  requestJSON,
  scale,
  schemaParameters,
  segmentLabels,
  segments,
  toolGroupsOf,
} from "@/features/context/segments";
import type { SegmentKind, ToolGroup, View } from "@/features/context/segments";
import { LayerChip } from "@/features/profiles/fields";
import { formatAgo, formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

export type ContextInspectorProps = {
  sessionId: string;
  chat: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  view: View;
  onView: (view: View) => void;
  requests: RequestSelection;
  config: SessionConfiguration | undefined;
};

export function ContextInspector({
  sessionId,
  chat,
  open,
  onOpenChange,
  view,
  onView,
  requests,
  config,
}: ContextInspectorProps) {
  const { shown } = requests;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[min(92vh,56rem)] flex-col gap-0 p-0 sm:max-w-6xl">
        <DialogHeader className="border-b px-4 py-3 pr-12">
          <DialogTitle>Context inspector</DialogTitle>
          <DialogDescription>
            Everything one model request sends, part by part. Sizes are estimates, four bytes to a
            token.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap items-center gap-2 border-b px-4 py-2">
          <RequestSelect id="inspector-request" requests={requests} className="w-full sm:w-80" />
          {shown.data !== undefined && <CopyButton context={shown.data} />}
        </div>
        {shown.isPending && (
          <div className="p-4">
            <Notice tone="pending">Assembling the request…</Notice>
          </div>
        )}
        {shown.isError && (
          <div className="p-4">
            <LoadError
              what={requests.selected === nextRequest ? "the next request" : "the recorded request"}
              error={shown.error}
              retrying={shown.isFetching}
              retry={() => void shown.refetch()}
            />
          </div>
        )}
        {shown.data !== undefined && (
          <Inspection
            sessionId={sessionId}
            chat={chat}
            context={shown.data}
            live={requests.selected === nextRequest}
            config={config}
            view={view}
            onView={onView}
            onEdit={() => {
              onOpenChange(false);
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

export type RequestSelectProps = {
  id: string;
  requests: RequestSelection;
  className?: string;
};

/** RequestSelect picks the request shown: the next one, live, or a recorded call. */
export function RequestSelect({ id, requests, className }: RequestSelectProps) {
  const list = requests.records.data ?? [];
  return (
    <>
      <Select value={requests.selected} onValueChange={requests.select}>
        <SelectTrigger id={id} size="sm" aria-label="Request" className={cn("text-xs", className)}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={nextRequest} className="text-xs">
            Next request, live
          </SelectItem>
          {[...list].reverse().map((r, i) => (
            <SelectItem key={r.id} value={r.id} className="text-xs">
              {requestLabel(r, list.length - i)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {requests.records.isError && (
        <LoadError
          what="the recorded requests"
          error={requests.records.error}
          retrying={requests.records.isFetching}
          retry={() => void requests.records.refetch()}
        />
      )}
    </>
  );
}

/** requestLabel names a recorded call in the select: its number, model, and measured input. */
function requestLabel(r: ModelRequest, n: number): string {
  const measured = r.input_tokens > 0 ? ` · ${formatTokens(r.input_tokens)} in` : "";
  return `#${String(n)} · ${r.model}${measured} · ${formatAgo(r.created_at)}`;
}

/** CopyButton copies the request as one JSON document. */
function CopyButton({ context }: { context: ModelContext }) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  return (
    <div className="ml-auto flex items-center gap-2">
      {failed && <span className="text-destructive text-xs">Could not copy: no clipboard</span>}
      <Button
        type="button"
        size="sm"
        variant="outline"
        title="The request as the harness hands it to the provider, before the provider adapts it to its endpoint"
        onClick={() => {
          const text = JSON.stringify(requestJSON(context), null, 2);
          navigator.clipboard.writeText(text).then(
            () => {
              setFailed(false);
              setCopied(true);
              setTimeout(() => {
                setCopied(false);
              }, 2000);
            },
            () => {
              setFailed(true);
            },
          );
        }}
      >
        {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
        {copied ? "Copied" : "Copy as JSON"}
      </Button>
    </div>
  );
}

type InspectionProps = {
  sessionId: string;
  chat: boolean;
  context: ModelContext;
  /** live says the request is the next one, whose parts link to their settings. */
  live: boolean;
  config: SessionConfiguration | undefined;
  view: View;
  onView: (view: View) => void;
  onEdit: () => void;
};

/** NavItem is one row of the part column. */
type NavItem = {
  view: View;
  label: string;
  tokens?: number;
  kind?: SegmentKind;
  depth: 0 | 1 | 2;
  mono?: boolean;
};

/** navItems lists a request's parts as the column shows them. */
function navItems(context: ModelContext): NavItem[] {
  const sections = context.sections ?? [];
  const tools = context.tools ?? [];
  const messages = context.messages ?? [];
  const items: NavItem[] = [
    {
      view: "system",
      label: "System prompt",
      tokens: sections.reduce((n, s) => n + s.tokens, 0),
      depth: 0,
    },
  ];
  for (const s of sections) {
    items.push({
      view: `section:${s.kind}`,
      label: segmentLabels[s.kind],
      tokens: s.tokens,
      kind: s.kind,
      depth: 1,
    });
    for (const f of s.files ?? []) {
      items.push({ view: `file:${f.path}`, label: f.path, tokens: f.tokens, depth: 2, mono: true });
    }
  }
  items.push({
    view: "tools",
    label: `Tools (${String(tools.length)})`,
    tokens: tools.reduce((n, t) => n + t.tokens, 0),
    depth: 0,
  });
  for (const g of toolGroupsOf(tools)) {
    items.push({
      view: `group:${g.id}`,
      label: g.label,
      tokens: g.tokens,
      kind: g.id === "builtin" ? "builtin_tools" : "mcp_tools",
      depth: 1,
      mono: g.server !== undefined,
    });
  }
  items.push({
    view: "messages",
    label: `Messages (${String(messages.length)})`,
    tokens: context.message_tokens,
    kind: "messages",
    depth: 0,
  });
  items.push({ view: "parameters", label: "Parameters", depth: 0 });
  return items;
}

function Inspection({
  sessionId,
  chat,
  context,
  live,
  config,
  view,
  onView,
  onEdit,
}: InspectionProps) {
  const parts = segments(context);
  const total = estimatedTotal(parts);
  const ratio = scale(context);
  const items = navItems(context);
  const current = items.some((i) => i.view === view) ? view : "system";
  const edit = { sessionId, config: live ? config : undefined, onOpen: onEdit };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="space-y-2 border-b px-4 py-3">
        <p className="flex flex-wrap items-baseline gap-x-2 text-xs">
          <span className="font-mono text-lg font-semibold tabular-nums">
            ~{formatTokens(total)}
          </span>
          <span className="text-muted-foreground">tokens estimated</span>
          {ratio !== undefined && (
            <span className="text-muted-foreground">
              · <span className="font-mono tabular-nums">~{formatTokens(total * ratio)}</span>{" "}
              scaled to the last measured call
            </span>
          )}
          {(context.request?.input_tokens ?? 0) > 0 && (
            <span className="text-muted-foreground">
              ·{" "}
              <span className="font-mono tabular-nums">
                {(context.request?.input_tokens ?? 0).toLocaleString("en-US")}
              </span>{" "}
              measured by the endpoint
            </span>
          )}
        </p>
        <WindowMeter context={context} total={total} />
        <PartsBar
          parts={parts}
          total={total}
          onOpen={(kind) => {
            onView(viewOf(kind));
          }}
        />
        {context.context_files_unread !== undefined && context.context_files_unread !== "" && (
          <Notice>
            The workspace is not running, so its context files are not read here. A run starts it
            and reads them.
          </Notice>
        )}
        {context.dropped_effort !== undefined && context.dropped_effort !== "" && (
          <Notice>
            The reasoning effort <span className="font-mono">{context.dropped_effort}</span> is not
            one the model offers, so it is not sent.
          </Notice>
        )}
      </div>
      <div className="grid min-h-0 flex-1 grid-rows-[auto_1fr] sm:grid-cols-[15rem_1fr] sm:grid-rows-1">
        <div className="border-b px-4 py-2 sm:hidden">
          <Select
            value={current}
            onValueChange={(v) => {
              onView(v as View);
            }}
          >
            <SelectTrigger size="sm" aria-label="Part" className="w-full text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {items.map((item) => (
                <SelectItem key={item.view} value={item.view} className="text-xs">
                  {"  ".repeat(item.depth)}
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <nav aria-label="Parts" className="hidden overflow-y-auto border-r p-2 sm:block">
          <ul className="space-y-px">
            {items.map((item) => (
              <li key={item.view}>
                <button
                  type="button"
                  aria-current={current === item.view ? "true" : undefined}
                  onClick={() => {
                    onView(item.view);
                  }}
                  className={cn(
                    "hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-2 rounded-md py-1 pr-2 text-left text-xs transition-colors focus-visible:ring-1 focus-visible:outline-none",
                    item.depth === 0 && "pl-2 font-medium",
                    item.depth === 1 && "pl-5",
                    item.depth === 2 && "pl-8",
                    current === item.view && "bg-accent",
                  )}
                >
                  {item.kind !== undefined && item.depth > 0 && <Dot kind={item.kind} />}
                  <span
                    className={cn("min-w-0 flex-1 truncate", item.mono === true && "font-mono")}
                  >
                    {item.label}
                  </span>
                  {item.tokens !== undefined && (
                    <span className="text-muted-foreground shrink-0 font-mono text-2xs tabular-nums">
                      {formatTokens(item.tokens)}
                    </span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        </nav>
        <div className="min-h-0 overflow-y-auto p-4" role="region" aria-label="Part detail">
          <Pane
            view={current}
            context={context}
            total={total}
            chat={chat}
            edit={edit}
            onView={onView}
          />
        </div>
      </div>
    </div>
  );
}

/** Edit is what a pane needs to link a part to its setting; config is absent for a record. */
type Edit = {
  sessionId: string;
  config: SessionConfiguration | undefined;
  onOpen: () => void;
};

type PaneProps = {
  view: View;
  context: ModelContext;
  total: number;
  chat: boolean;
  edit: Edit;
  onView: (view: View) => void;
};

function Pane({ view, context, total, chat, edit, onView }: PaneProps) {
  const sections = context.sections ?? [];
  const tools = context.tools ?? [];
  if (view === "system") {
    return (
      <PaneFrame
        title="System prompt"
        tokens={sections.reduce((n, s) => n + s.tokens, 0)}
        total={total}
        note={`${String(sections.length)} section${sections.length === 1 ? "" : "s"}, joined by a blank line, in the order the model reads them.`}
      >
        {sections.length === 0 && (
          <p className="text-muted-foreground text-sm">No system prompt.</p>
        )}
        {sections.map((s) => (
          <SectionBlock key={s.kind} section={s} chat={chat} edit={edit} />
        ))}
      </PaneFrame>
    );
  }
  if (view.startsWith("section:")) {
    const section = sections.find((s) => `section:${s.kind}` === view);
    if (section === undefined) return null;
    return (
      <PaneFrame title={segmentLabels[section.kind]} tokens={section.tokens} total={total}>
        <SectionBlock section={section} chat={chat} edit={edit} bare />
      </PaneFrame>
    );
  }
  if (view.startsWith("file:")) {
    const path = view.slice("file:".length);
    const file = sections.flatMap((s) => s.files ?? []).find((f) => f.path === path);
    if (file === undefined) return null;
    return (
      <PaneFrame
        title={file.path}
        mono
        tokens={file.tokens}
        total={total}
        note="A context file, as the model reads it."
      >
        <TextBlock label={file.path}>{file.text}</TextBlock>
      </PaneFrame>
    );
  }
  if (view === "tools" || view.startsWith("group:")) {
    const groups = toolGroupsOf(tools);
    const group = groups.find((g) => `group:${g.id}` === view);
    const shown = group === undefined ? groups : [group];
    const tokens = shown.reduce((n, g) => n + g.tokens, 0);
    return (
      <PaneFrame
        title={
          group === undefined
            ? "Tools"
            : group.server === undefined
              ? `${group.label} tools`
              : group.label
        }
        mono={group?.server !== undefined}
        tokens={tokens}
        total={total}
        note="Every definition is sent with every request, whether the model calls the tool or not."
        action={<EditLinkFor edit={edit} kind="builtin_tools" chat={chat} what="the tools" />}
      >
        {group === undefined && tools.length > 0 && <ToolSizes groups={groups} onView={onView} />}
        <ToolList groups={shown} />
      </PaneFrame>
    );
  }
  if (view === "messages") {
    return (
      <PaneFrame
        title="Messages"
        tokens={context.message_tokens}
        total={total}
        note={
          context.request === undefined
            ? "The conversation so far. A message sent now is added after these."
            : "The conversation this call sent."
        }
      >
        <Messages messages={context.messages ?? []} sizes={context.message_sizes ?? []} />
      </PaneFrame>
    );
  }
  return (
    <PaneFrame
      title="Parameters"
      note="What the request sends beside its content, and the layer each came from."
    >
      <Parameters context={context} edit={edit} />
    </PaneFrame>
  );
}

type PaneFrameProps = {
  title: string;
  mono?: boolean;
  tokens?: number;
  total?: number;
  note?: string;
  action?: React.ReactNode;
  children: React.ReactNode;
};

/** PaneFrame is a pane's heading, its size and share, a note, and its content. */
function PaneFrame({ title, mono = false, tokens, total, note, action, children }: PaneFrameProps) {
  return (
    <div className="space-y-3">
      <div className="space-y-0.5">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <h3 className={cn("text-base font-semibold", mono && "font-mono")}>{title}</h3>
          {tokens !== undefined && (
            <span className="text-muted-foreground font-mono text-xs tabular-nums">
              ~{formatTokens(tokens)} tokens
              {total !== undefined && total > 0 && ` · ${percent(tokens, total)} of the request`}
            </span>
          )}
          <div className="ml-auto">{action}</div>
        </div>
        {note !== undefined && <p className="text-muted-foreground text-xs">{note}</p>}
      </div>
      {children}
    </div>
  );
}

/** layerOf is the layer a setting of the next request came from. */
function layerOf(config: SessionConfiguration | undefined, key: string): ConfigLayer | undefined {
  return config?.resolved.sources?.[key];
}

type EditLinkForProps = { edit: Edit; kind: SegmentKind; chat: boolean; what: string };

/** EditLinkFor links a part of the next request to the setting behind it; a record links nowhere. */
function EditLinkFor({ edit, kind, chat, what }: EditLinkForProps) {
  const target = editTargetOf(kind, chat);
  if (target === undefined || edit.config === undefined) return null;
  return <EditLink {...edit} target={target} what={what} />;
}

type SectionBlockProps = {
  section: NonNullable<ModelContext["sections"]>[number];
  chat: boolean;
  edit: Edit;
  /** bare leaves out the header, for a pane that shows only this section. */
  bare?: boolean;
};

/** SectionBlock is one section of the system prompt, marked with its colour and source. */
function SectionBlock({ section, chat, edit, bare = false }: SectionBlockProps) {
  const target = editTargetOf(section.kind, chat);
  const layer = target === undefined ? undefined : layerOf(edit.config, target.key);
  const label = segmentLabels[section.kind];
  // The overview shows each section exactly as sent; a context files
  // section's own view splits it into its files.
  const body =
    bare && section.files !== undefined && section.files.length > 0 ? (
      <div className="space-y-2">
        {section.files.map((f) => (
          <div key={f.path} className="space-y-1">
            <p className="flex items-center gap-2 text-xs">
              <span className="font-mono font-medium">{f.path}</span>
              <span className="text-muted-foreground font-mono tabular-nums">
                ~{formatTokens(f.tokens)}
              </span>
            </p>
            <TextBlock label={f.path}>{f.text}</TextBlock>
          </div>
        ))}
      </div>
    ) : (
      <TextBlock label={label}>{section.text}</TextBlock>
    );
  if (bare) {
    return (
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          {layer !== undefined && <LayerChip set={false} from={layer} />}
          <div className="ml-auto">
            <EditLinkFor edit={edit} kind={section.kind} chat={chat} what={label.toLowerCase()} />
          </div>
        </div>
        {body}
      </div>
    );
  }
  return (
    <article className="flex gap-3" aria-label={label}>
      <span aria-hidden className={cn("w-1 shrink-0 rounded-full", segmentColors[section.kind])} />
      <div className="min-w-0 flex-1 space-y-1.5">
        <header className="flex flex-wrap items-center gap-2">
          <h4 className="text-sm font-medium">{label}</h4>
          {layer !== undefined && <LayerChip set={false} from={layer} />}
          <span className="text-muted-foreground font-mono text-xs tabular-nums">
            ~{formatTokens(section.tokens)}
          </span>
          <div className="ml-auto">
            <EditLinkFor edit={edit} kind={section.kind} chat={chat} what={label.toLowerCase()} />
          </div>
        </header>
        {body}
      </div>
    </article>
  );
}

/** TextBlock is prompt text as the model reads it: wrapped, monospace, whole. */
function TextBlock({ label, children }: { label: string; children: string }) {
  return (
    <OutputBlock label={label} maxHeightClass="max-h-none" className="whitespace-pre-wrap">
      {children}
    </OutputBlock>
  );
}

type ToolSizesProps = { groups: readonly ToolGroup[]; onView: (view: View) => void };

/** ToolSizes ranks the tools by what their definitions cost, the largest first. */
function ToolSizes({ groups, onView }: ToolSizesProps) {
  const ranked = groups
    .flatMap((g) => g.tools.map((t) => ({ tool: t, group: g })))
    .sort((a, b) => b.tool.tokens - a.tool.tokens)
    .slice(0, 8);
  const largest = ranked[0]?.tool.tokens ?? 0;
  return (
    <section aria-label="The largest tools" className="rounded-md border p-3">
      <h4 className="text-muted-foreground mb-2 text-2xs font-semibold tracking-wide uppercase">
        Largest definitions
      </h4>
      <ul className="space-y-1">
        {ranked.map(({ tool, group }) => (
          <li key={tool.name}>
            <button
              type="button"
              onClick={() => {
                onView(`group:${group.id}`);
              }}
              className="hover:bg-accent focus-visible:ring-ring grid w-full grid-cols-[minmax(0,14rem)_1fr_3.5rem] items-center gap-3 rounded-md px-1 py-0.5 text-left text-xs transition-colors focus-visible:ring-1 focus-visible:outline-none"
            >
              <span className="truncate font-mono">{tool.name}</span>
              <span className="bg-muted h-2 overflow-hidden rounded-full">
                <span
                  className={cn(
                    "block h-full rounded-full",
                    group.server === undefined && group.id === "builtin"
                      ? "bg-chart-5"
                      : "bg-chart-4",
                  )}
                  style={{ width: `${String(largest > 0 ? (tool.tokens / largest) * 100 : 0)}%` }}
                />
              </span>
              <span className="text-muted-foreground text-right font-mono tabular-nums">
                ~{formatTokens(tool.tokens)}
              </span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** ToolList is every tool of the groups, with a search over them. */
function ToolList({ groups }: { groups: readonly ToolGroup[] }) {
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const match = (t: ToolSchema) =>
    q === "" || t.name.toLowerCase().includes(q) || t.description.toLowerCase().includes(q);
  const shown = groups
    .map((g) => ({ ...g, tools: g.tools.filter(match) }))
    .filter((g) => g.tools.length > 0);
  return (
    <div className="space-y-3">
      <Input
        type="search"
        aria-label="Search the tools"
        placeholder="Search the tools"
        value={query}
        className="h-7 text-xs"
        onChange={(e) => {
          setQuery(e.target.value);
        }}
      />
      {shown.length === 0 && (
        <p className="text-muted-foreground text-sm">
          {groups.length === 0 ? "The request offers no tools." : "No tool matches the search."}
        </p>
      )}
      {shown.map((g) => (
        <section
          key={g.id}
          className="space-y-2"
          aria-label={g.server === undefined ? `${g.label} tools` : `Tools of ${g.label}`}
        >
          {groups.length > 1 && (
            <h4
              className={cn(
                "flex items-center gap-2 text-sm font-medium",
                g.server !== undefined && "font-mono",
              )}
            >
              <Dot kind={g.id === "builtin" ? "builtin_tools" : "mcp_tools"} />
              {g.label}
              <span className="text-muted-foreground font-sans text-xs font-normal">
                {g.tools.length} · ~{formatTokens(g.tokens)}
              </span>
            </h4>
          )}
          {g.tools.map((t) => (
            <ToolCard key={t.name} tool={t} server={g.server} />
          ))}
        </section>
      ))}
    </div>
  );
}

/** ToolCard is one tool definition: its name, description, and parameters, or its raw schema. */
function ToolCard({ tool, server }: { tool: ToolSchema; server: string | undefined }) {
  const [raw, setRaw] = useState(false);
  const params = schemaParameters(tool.schema);
  const short = server === undefined ? tool.name : tool.name.replace(`mcp__${server}__`, "");
  return (
    <article className="overflow-hidden rounded-md border" aria-label={tool.name}>
      <header className="bg-muted/40 flex flex-wrap items-center gap-2 border-b px-3 py-1.5">
        <h5 className="font-mono text-sm font-medium" title={tool.name}>
          {short}
        </h5>
        <span className="text-muted-foreground font-mono text-xs tabular-nums">
          ~{formatTokens(tool.tokens)}
        </span>
        <Button
          type="button"
          size="xs"
          variant={raw ? "secondary" : "ghost"}
          aria-pressed={raw}
          className="ml-auto"
          onClick={() => {
            setRaw(!raw);
          }}
        >
          JSON<span className="sr-only"> schema of {tool.name}</span>
        </Button>
      </header>
      <div className="space-y-2 px-3 py-2">
        <p className="text-sm whitespace-pre-wrap">{tool.description}</p>
        {raw ? (
          <OutputBlock label={`Schema of ${tool.name}`} maxHeightClass="max-h-80">
            {JSON.stringify(tool.schema, null, 2)}
          </OutputBlock>
        ) : params.length === 0 ? (
          <p className="text-muted-foreground text-xs">No parameters.</p>
        ) : (
          <table className="w-full text-xs">
            <caption className="sr-only">Parameters of {tool.name}</caption>
            <thead>
              <tr className="text-muted-foreground border-b text-left">
                <th className="py-1 pr-3 font-medium">Parameter</th>
                <th className="py-1 pr-3 font-medium">Type</th>
                <th className="py-1 font-medium">Description</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {params.map((p) => (
                <tr key={p.name} className="align-top">
                  <td className="py-1.5 pr-3 whitespace-nowrap">
                    <span className="font-mono font-medium">{p.name}</span>
                    {p.required && (
                      <span className="text-primary ml-1.5 text-2xs font-medium">required</span>
                    )}
                  </td>
                  <td className="text-muted-foreground py-1.5 pr-3 font-mono">
                    {p.type}
                    {p.values !== undefined && (
                      <span className="block text-2xs">{p.values.join(" | ")}</span>
                    )}
                  </td>
                  <td className="py-1.5">{p.description}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </article>
  );
}

/** roleTone is how each author's messages are marked. */
const roleTone: Record<Message["role"], string> = {
  user: "border-primary/40 bg-primary/10 text-primary",
  assistant: "bg-muted text-foreground border-transparent",
  tool: "border-chart-3/40 bg-chart-3/10 text-chart-3",
  system: "bg-muted text-muted-foreground border-transparent",
};

type MessagesProps = { messages: readonly Message[]; sizes: readonly number[] };

/** Messages is the conversation as the request sends it, every message whole. */
function Messages({ messages, sizes }: MessagesProps) {
  const [role, setRole] = useState<Message["role"] | "all">("all");
  if (messages.length === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        No messages yet: the next one starts the conversation.
      </p>
    );
  }
  const names = new Map<string, string>();
  for (const m of messages) for (const c of m.tool_calls ?? []) names.set(c.id, c.name);
  const largest = Math.max(...sizes, 1);
  const roles = (["user", "assistant", "tool"] as const).filter((r) =>
    messages.some((m) => m.role === r),
  );
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-1" role="group" aria-label="Show messages by">
        {(["all", ...roles] as const).map((r) => (
          <Button
            key={r}
            type="button"
            size="xs"
            variant={role === r ? "secondary" : "ghost"}
            aria-pressed={role === r}
            onClick={() => {
              setRole(r);
            }}
          >
            {r === "all" ? "All" : r}
            <span className="text-muted-foreground font-mono tabular-nums">
              {r === "all" ? messages.length : messages.filter((m) => m.role === r).length}
            </span>
          </Button>
        ))}
      </div>
      <ol className="space-y-2">
        {messages.map((m, i) =>
          role !== "all" && m.role !== role ? null : (
            // The messages are a fixed list for this request; their order is their identity.
            <li key={`${String(i)}:${m.role}`}>
              <MessageCard
                n={i + 1}
                message={m}
                size={sizes[i] ?? 0}
                largest={largest}
                toolName={m.tool_call_id === undefined ? undefined : names.get(m.tool_call_id)}
              />
            </li>
          ),
        )}
      </ol>
    </div>
  );
}

type MessageCardProps = {
  n: number;
  message: Message;
  size: number;
  largest: number;
  toolName: string | undefined;
};

/** longMessage is how many characters a message shows before it folds. */
const longMessage = 1200;

function MessageCard({ n, message: m, size, largest, toolName }: MessageCardProps) {
  const [whole, setWhole] = useState(false);
  const content = m.content ?? "";
  const folded = !whole && content.length > longMessage;
  return (
    <article
      className="overflow-hidden rounded-md border"
      aria-label={`Message ${String(n)}, ${m.role}`}
    >
      <header className="flex flex-wrap items-center gap-2 border-b px-3 py-1.5">
        <span className="text-muted-foreground font-mono text-2xs tabular-nums">#{n}</span>
        <span className={cn("rounded-full border px-1.5 text-2xs font-medium", roleTone[m.role])}>
          {m.role}
        </span>
        {toolName !== undefined && (
          <span className="text-muted-foreground font-mono text-xs">result of {toolName}</span>
        )}
        {m.is_error === true && <span className="text-destructive text-xs">error</span>}
        <span className="ml-auto flex items-center gap-2">
          <span
            className="bg-muted hidden h-1.5 w-16 overflow-hidden rounded-full sm:block"
            aria-hidden
          >
            <span
              className="bg-primary/60 block h-full"
              style={{ width: `${String((size / largest) * 100)}%` }}
            />
          </span>
          <span className="text-muted-foreground font-mono text-xs tabular-nums">
            ~{formatTokens(size)}
          </span>
        </span>
      </header>
      <div className="space-y-2 px-3 py-2">
        {m.reasoning !== undefined && m.reasoning !== "" && (
          <div className="space-y-0.5">
            <p className="text-muted-foreground text-2xs font-semibold tracking-wide uppercase">
              Reasoning, replayed
            </p>
            <p className="text-muted-foreground text-xs whitespace-pre-wrap italic">
              {m.reasoning}
            </p>
          </div>
        )}
        {content !== "" && (
          <p
            className={cn(
              "text-sm break-words whitespace-pre-wrap",
              m.role === "tool" && "font-mono text-xs",
              m.is_error === true && "text-destructive",
            )}
          >
            {folded ? `${content.slice(0, longMessage)}…` : content}
          </p>
        )}
        {content.length > longMessage && (
          <Button
            type="button"
            size="xs"
            variant="ghost"
            onClick={() => {
              setWhole(!whole);
            }}
          >
            {whole ? "Show less" : `Show all ${formatTokens(content.length)} characters`}
          </Button>
        )}
        {(m.tool_calls ?? []).map((c) => (
          <div key={c.id} className="space-y-1">
            <p className="text-xs">
              Calls <span className="font-mono font-medium">{c.name}</span>
              <span className="text-muted-foreground font-mono"> {c.id}</span>
            </p>
            <OutputBlock label={`Arguments of ${c.name}`} maxHeightClass="max-h-60">
              {c.arguments_malformed === true
                ? String(c.arguments)
                : JSON.stringify(c.arguments, null, 2)}
            </OutputBlock>
          </div>
        ))}
      </div>
    </article>
  );
}

/** Parameters is what the request sends beside its content, and where each came from. */
function Parameters({ context, edit }: { context: ModelContext; edit: Edit }) {
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
    <table className="w-full text-sm">
      <thead>
        <tr className="text-muted-foreground border-b text-left text-xs">
          <th className="py-1 pr-3 font-medium">Parameter</th>
          <th className="py-1 pr-3 font-medium">Value</th>
          <th className="py-1 pr-3 font-medium">From</th>
          <th className="py-1">
            <span className="sr-only">Edit</span>
          </th>
        </tr>
      </thead>
      <tbody className="divide-y">
        {rows.map((r) => {
          const layer = context.sources?.[r.key];
          const target = parameterTarget(r.key);
          return (
            <tr key={r.name}>
              <td className="py-1.5 pr-3 font-mono text-xs">{r.name}</td>
              <td className="py-1.5 pr-3 font-mono text-xs break-all">
                {r.value === "" ? "none" : r.value}
              </td>
              <td className="py-1.5 pr-3">
                {layer !== undefined && <LayerChip set={false} from={layer} />}
              </td>
              <td className="py-1 text-right">
                {target !== undefined && edit.config !== undefined && (
                  <EditLink {...edit} target={target} what={r.name} compact />
                )}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
