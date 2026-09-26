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
  estimatedTotal,
  scale,
  segmentLabels,
  segments,
  toolGroupsOf,
  viewOf,
} from "@/features/context/segments";
import type { SegmentKind, View } from "@/features/context/segments";
import { formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

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

export function Inspection({
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
