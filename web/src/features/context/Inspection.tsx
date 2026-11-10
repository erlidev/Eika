/**
 * The inspection: a request's totals and meters, its parts in a column, and
 * the chosen part beside them.
 */

import type { ModelContext, SessionConfiguration } from "@/api/types";
import { Notice } from "@/components/Notice";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Pane } from "@/features/context/Pane";
import { Dot, PartsBar, WindowMeter } from "@/features/context/parts";
import {
  barTotal,
  remainderNote,
  requestSize,
  segmentLabels,
  segments,
  sizedSections,
  sizedTools,
  tilde,
  toolGroupsOf,
  viewOf,
} from "@/features/context/segments";
import type { SegmentKind, View } from "@/features/context/segments";
import { CallFacts, DriftNotice, PartsNotes, RequestHeadline } from "@/features/context/Size";
import { formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

type InspectionProps = {
  sessionId: string;
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
  /** approximate says the size is not the endpoint's own count. */
  approximate?: boolean;
  kind?: SegmentKind;
  depth: 0 | 1 | 2;
  mono?: boolean;
};

/** navItems lists a request's parts as the column shows them. */
function navItems(context: ModelContext): NavItem[] {
  const sections = sizedSections(context);
  const tools = sizedTools(context);
  const messages = context.messages ?? [];
  const sizes = context.breakdown.messages;
  const items: NavItem[] = [
    {
      view: "system",
      label: "System prompt",
      tokens: sections.reduce((n, s) => n + s.tokens, 0),
      approximate: sections.some((s) => s.approximate),
      depth: 0,
    },
  ];
  for (const s of sections) {
    items.push({
      view: `section:${s.kind}`,
      label: segmentLabels[s.kind],
      tokens: s.tokens,
      approximate: s.approximate,
      kind: s.kind,
      depth: 1,
    });
  }
  items.push({
    view: "tools",
    label: `Tools (${String(tools.length)})`,
    tokens: tools.reduce((n, t) => n + t.tokens, 0),
    approximate: tools.some((t) => t.approximate),
    depth: 0,
  });
  for (const g of toolGroupsOf(tools)) {
    items.push({
      view: `group:${g.id}`,
      label: g.label,
      tokens: g.tokens,
      approximate: g.approximate,
      kind: g.id === "builtin" ? "builtin_tools" : "mcp_tools",
      depth: 1,
      mono: g.server !== undefined,
    });
  }
  items.push({
    view: "messages",
    label: `Messages (${String(messages.length)})`,
    tokens: sizes.reduce((n, m) => n + m.tokens + (m.reasoning_tokens ?? 0), 0),
    approximate: sizes.some((m) => m.approximate === true),
    kind: "messages",
    depth: 0,
  });
  items.push({ view: "parameters", label: "Parameters", depth: 0 });
  return items;
}

export function Inspection({
  sessionId,
  context,
  live,
  config,
  view,
  onView,
  onEdit,
}: InspectionProps) {
  const parts = segments(context);
  const total = barTotal(parts);
  const items = navItems(context);
  const current = items.some((i) => i.view === view) ? view : "system";
  const edit = { sessionId, config: live ? config : undefined, onOpen: onEdit };
  const remainder = context.breakdown.remainder;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="space-y-2 border-b px-4 py-3 text-xs">
        <RequestHeadline context={context} />
        <DriftNotice context={context} />
        <CallFacts context={context} />
        <WindowMeter context={context} used={requestSize(context).tokens} />
        <PartsBar
          parts={parts}
          total={total}
          onOpen={(kind) => {
            onView(viewOf(kind));
          }}
        />
        <PartsNotes context={context} />
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
                      {tilde(item.approximate === true)}
                      {formatTokens(item.tokens)}
                    </span>
                  )}
                </button>
              </li>
            ))}
          </ul>
          <div
            className="mt-2 flex items-center gap-2 border-t px-2 pt-2 text-xs"
            title={remainderNote(context)}
          >
            <Dot kind="template" />
            <span className="min-w-0 flex-1 truncate">Template and formatting</span>
            <span
              className={cn(
                "text-muted-foreground shrink-0 font-mono text-2xs tabular-nums",
                remainder !== null && remainder < 0 && "text-destructive",
              )}
            >
              {remainder === null
                ? "n/a"
                : remainder < 0
                  ? `-${formatTokens(-remainder)}`
                  : formatTokens(remainder)}
            </span>
          </div>
        </nav>
        <div className="min-h-0 overflow-y-auto p-4" role="region" aria-label="Part detail">
          <Pane view={current} context={context} total={total} edit={edit} onView={onView} />
        </div>
      </div>
    </div>
  );
}
