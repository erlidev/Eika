/**
 * The pane beside the part column: the chosen part whole, from the system
 * prompt section by section to the parameters and the layer each came from.
 */

import type { ConfigLayer, ModelContext, SessionConfiguration } from "@/api/types";
import { OutputBlock } from "@/components/OutputBlock";
import { Messages } from "@/features/context/Messages";
import { EditLink } from "@/features/context/parts";
import {
  editTargetOf,
  messageSizes,
  parameterTarget,
  percent,
  segmentColors,
  segmentLabels,
  sizedSections,
  sizedTools,
  tilde,
  toolGroupsOf,
} from "@/features/context/segments";
import type { SegmentKind, SizedSection, View } from "@/features/context/segments";
import { ToolList, ToolSizes } from "@/features/context/ToolList";
import { LayerChip } from "@/features/profiles/fields";
import { formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

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
  edit: Edit;
  onView: (view: View) => void;
};

export function Pane({ view, context, total, edit, onView }: PaneProps) {
  const sections = sizedSections(context);
  const tools = sizedTools(context);
  if (view === "system") {
    return (
      <PaneFrame
        title="System prompt"
        tokens={sections.reduce((n, s) => n + s.tokens, 0)}
        approximate={sections.some((s) => s.approximate)}
        total={total}
        note={`${String(sections.length)} section${sections.length === 1 ? "" : "s"}, joined by a blank line, in the order the model reads them.`}
      >
        {sections.length === 0 && (
          <p className="text-muted-foreground text-sm">No system prompt.</p>
        )}
        {sections.map((s) => (
          <SectionBlock key={s.kind} section={s} edit={edit} />
        ))}
      </PaneFrame>
    );
  }
  if (view.startsWith("section:")) {
    const section = sections.find((s) => `section:${s.kind}` === view);
    if (section === undefined) return null;
    return (
      <PaneFrame
        title={segmentLabels[section.kind]}
        tokens={section.tokens}
        approximate={section.approximate}
        total={total}
      >
        <SectionBlock section={section} edit={edit} bare />
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
        approximate={shown.some((g) => g.approximate)}
        total={total}
        note={`Every definition is sent with every request, whether the model calls the tool or not.${
          tools.some((t) => t.approximate)
            ? " The endpoint cannot render this request with and without its tools, so each size is the definition counted as JSON text."
            : " Each size is what the template adds when the tool is added to those before it; the first carries the template's wrapper around the list."
        }`}
        action={<EditLinkFor edit={edit} kind="builtin_tools" what="the tools" />}
      >
        {group === undefined && tools.length > 0 && <ToolSizes groups={groups} onView={onView} />}
        <ToolList groups={shown} />
      </PaneFrame>
    );
  }
  if (view === "messages") {
    const sizes = messageSizes(context);
    const reasoning = sizes.reduce((n, m) => n + (m.reasoning_tokens ?? 0), 0);
    return (
      <PaneFrame
        title="Messages"
        tokens={sizes.reduce((n, m) => n + m.tokens + (m.reasoning_tokens ?? 0), 0)}
        approximate={sizes.some((m) => m.approximate === true)}
        total={total}
        note={`${
          context.call === undefined
            ? "The conversation so far. A message sent now is added after these."
            : "The conversation this call sent."
        }${reasoning > 0 ? ` Includes ${formatTokens(reasoning)} tokens of replayed reasoning, counted apart from the messages' text.` : ""}`}
      >
        <Messages messages={context.messages ?? []} sizes={sizes} />
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
  /** approximate says the size is not the endpoint's own count. */
  approximate?: boolean;
  total?: number;
  note?: string;
  action?: React.ReactNode;
  children: React.ReactNode;
};

/** PaneFrame is a pane's heading, its size and share, a note, and its content. */
function PaneFrame({
  title,
  mono = false,
  tokens,
  approximate = false,
  total,
  note,
  action,
  children,
}: PaneFrameProps) {
  return (
    <div className="space-y-3">
      <div className="space-y-0.5">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <h3 className={cn("text-base font-semibold", mono && "font-mono")}>{title}</h3>
          {tokens !== undefined && (
            <span className="text-muted-foreground font-mono text-xs tabular-nums">
              {tilde(approximate)}
              {formatTokens(tokens)} tokens
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

type EditLinkForProps = { edit: Edit; kind: SegmentKind; what: string };

/** EditLinkFor links a part of the next request to the setting behind it; a record links nowhere. */
function EditLinkFor({ edit, kind, what }: EditLinkForProps) {
  const target = editTargetOf(kind);
  if (target === undefined || edit.config === undefined) return null;
  return <EditLink {...edit} target={target} what={what} />;
}

type SectionBlockProps = {
  section: SizedSection;
  edit: Edit;
  /** bare leaves out the header, for a pane that shows only this section. */
  bare?: boolean;
};

/** SectionBlock is one section of the system prompt, marked with its colour and source. */
function SectionBlock({ section, edit, bare = false }: SectionBlockProps) {
  const target = editTargetOf(section.kind);
  const layer = target === undefined ? undefined : layerOf(edit.config, target.key);
  const label = segmentLabels[section.kind];
  const body = <TextBlock label={label}>{section.text}</TextBlock>;
  if (bare) {
    return (
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          {layer !== undefined && <LayerChip set={false} from={layer} />}
          <div className="ml-auto">
            <EditLinkFor edit={edit} kind={section.kind} what={label.toLowerCase()} />
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
            {tilde(section.approximate)}
            {formatTokens(section.tokens)}
          </span>
          <div className="ml-auto">
            <EditLinkFor edit={edit} kind={section.kind} what={label.toLowerCase()} />
          </div>
        </header>
        {body}
      </div>
    </article>
  );
}

type TextBlockProps = { label: string; children: string };

/** TextBlock is prompt text as the model reads it: wrapped, monospace, whole. */
function TextBlock({ label, children }: TextBlockProps) {
  return (
    <OutputBlock label={label} maxHeightClass="max-h-none" className="whitespace-pre-wrap">
      {children}
    </OutputBlock>
  );
}

type ParametersProps = { context: ModelContext; edit: Edit };

/** Parameters is what the request sends beside its content, and where each came from. */
function Parameters({ context, edit }: ParametersProps) {
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
    { name: "image_input", value: String(p.image_input), key: "image_input" },
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
