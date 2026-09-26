/**
 * The one control that chooses tools: the built-in ones and each MCP
 * server's, a switch for each with what offering it costs every request, a
 * search over them, and switches for a whole group. A server can be taken
 * whole, which keeps the tools it adds later. The configuration editor and a
 * chat's Tools panel both choose tools with it.
 */

import { Search } from "lucide-react";
import { useState } from "react";

import type { Tool } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  chosen,
  matchesTool,
  serverEntry,
  toolsTokens,
  withChoice,
} from "@/features/profiles/form";
import type { ToolGroups } from "@/features/profiles/form";
import { toolSummary } from "@/features/session/tools";
import { formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

export type ToolPickerProps = {
  /** idPrefix keeps the switch ids of two pickers on one page apart. */
  idPrefix: string;
  groups: ToolGroups;
  /** choice is the chosen entries: tool names, and `mcp__<server>__*` for a whole server. */
  choice: readonly string[];
  onChange: (choice: string[]) => void;
  /** readOnly shows the choice without letting it change, as one that falls through. */
  readOnly?: boolean;
  /** busy disables the switches while a change is on its way. */
  busy?: boolean;
};

export function ToolPicker({
  idPrefix,
  groups,
  choice,
  onChange,
  readOnly = false,
  busy = false,
}: ToolPickerProps) {
  const [query, setQuery] = useState("");
  const all = [...groups.builtin, ...groups.servers.flatMap((s) => s.tools)];
  const on = all.filter((t) => chosen(choice, t));
  const cost = toolsTokens(groups, choice);
  const disabled = readOnly || busy;
  const set = (entry: string, next: boolean) => {
    onChange(withChoice(choice, entry, next, groups));
  };
  const setMany = (tools: readonly Tool[], next: boolean) => {
    let out = [...choice];
    for (const t of tools) out = withChoice(out, t.name, next, groups);
    onChange(out);
  };
  const shown = (tools: readonly Tool[]) => tools.filter((t) => matchesTool(t, query));
  const builtin = shown(groups.builtin);
  const servers = groups.servers
    .map((s) => ({ ...s, shown: shown(s.tools) }))
    .filter((s) => s.shown.length > 0);

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-40 flex-1">
          <Search
            aria-hidden
            className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2"
          />
          <Input
            type="search"
            aria-label="Search the tools"
            placeholder="Search the tools"
            value={query}
            className="h-7 pl-7 text-xs"
            onChange={(e) => {
              setQuery(e.target.value);
            }}
          />
        </div>
        <p className="text-muted-foreground text-xs" aria-live="polite">
          <span className="text-foreground font-mono tabular-nums">{on.length}</span> of{" "}
          <span className="font-mono tabular-nums">{all.length}</span> on ·{" "}
          <span className="font-mono tabular-nums">~{formatTokens(cost)}</span> tokens a request
        </p>
      </div>

      {builtin.length === 0 && servers.length === 0 && (
        <p className="text-muted-foreground px-1 py-2 text-xs">No tool matches the search.</p>
      )}

      {builtin.length > 0 && (
        <ToolGroup
          title="Built-in"
          tools={builtin}
          choice={choice}
          disabled={disabled}
          actions={
            !readOnly && (
              <>
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  disabled={busy}
                  onClick={() => {
                    setMany(builtin, true);
                  }}
                >
                  All<span className="sr-only"> built-in tools</span>
                </Button>
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  disabled={busy}
                  onClick={() => {
                    setMany(builtin, false);
                  }}
                >
                  None<span className="sr-only"> of the built-in tools</span>
                </Button>
              </>
            )
          }
          idPrefix={idPrefix}
          onSet={set}
        />
      )}

      {servers.map(({ server, tools, shown: list }) => {
        const whole = serverEntry(server);
        const wholeId = `${idPrefix}-${whole}`;
        return (
          <ToolGroup
            key={server}
            title={server}
            mono
            badge="MCP"
            tools={list}
            choice={choice}
            disabled={disabled}
            idPrefix={idPrefix}
            onSet={set}
            actions={
              <div className="flex items-center gap-1.5">
                <Label
                  htmlFor={wholeId}
                  className="text-muted-foreground text-xs font-normal"
                  title="Take every tool of the server, including the ones it adds later"
                >
                  Whole server
                </Label>
                <Switch
                  id={wholeId}
                  size="sm"
                  aria-label={`Every tool of ${server}, and any it adds`}
                  checked={choice.includes(whole)}
                  disabled={disabled}
                  onCheckedChange={(next) => {
                    set(whole, next);
                  }}
                />
              </div>
            }
            footnote={
              list.length < tools.length
                ? `${String(tools.length - list.length)} more hidden by the search`
                : undefined
            }
          />
        );
      })}
    </div>
  );
}

type ToolGroupProps = {
  title: string;
  mono?: boolean;
  badge?: string;
  tools: readonly Tool[];
  choice: readonly string[];
  disabled: boolean;
  idPrefix: string;
  onSet: (entry: string, on: boolean) => void;
  actions?: React.ReactNode;
  footnote?: string | undefined;
};

/** ToolGroup is one bordered group of tools: its header, and a row for each tool. */
function ToolGroup({
  title,
  mono = false,
  badge,
  tools,
  choice,
  disabled,
  idPrefix,
  onSet,
  actions,
  footnote,
}: ToolGroupProps) {
  const on = tools.filter((t) => chosen(choice, t)).length;
  const label = `${mono ? "Tools of " : ""}${title}${mono ? "" : " tools"}`;
  return (
    <section className="overflow-hidden rounded-md border" aria-label={label}>
      <header className="bg-muted/40 flex min-h-8 flex-wrap items-center gap-x-2 gap-y-1 border-b px-2.5 py-1">
        <h4 className={cn("text-xs font-semibold", mono && "font-mono")}>{title}</h4>
        {badge !== undefined && (
          <span className="text-muted-foreground rounded-full border px-1.5 text-2xs">{badge}</span>
        )}
        <span className="text-muted-foreground font-mono text-2xs tabular-nums">
          {on}/{tools.length}
        </span>
        <div className="ml-auto flex items-center gap-1">{actions}</div>
      </header>
      <ul className="divide-y">
        {tools.map((t) => (
          <ToolRow
            key={t.name}
            id={`${idPrefix}-${t.name}`}
            tool={t}
            label={t.server === undefined ? t.name : t.name.replace(`mcp__${t.server}__`, "")}
            checked={chosen(choice, t)}
            disabled={disabled}
            onChange={(next) => {
              onSet(t.name, next);
            }}
          />
        ))}
      </ul>
      {footnote !== undefined && (
        <p className="text-muted-foreground border-t px-2.5 py-1 text-2xs">{footnote}</p>
      )}
    </section>
  );
}

type ToolRowProps = {
  id: string;
  tool: Tool;
  label: string;
  checked: boolean;
  disabled: boolean;
  onChange: (on: boolean) => void;
};

function ToolRow({ id, tool, label, checked, disabled, onChange }: ToolRowProps) {
  return (
    <li
      className={cn(
        "flex items-center gap-2.5 px-2.5 py-1.5 transition-opacity",
        !checked && "opacity-60",
      )}
    >
      <Switch
        id={id}
        size="sm"
        checked={checked}
        disabled={disabled}
        aria-label={label === tool.name ? undefined : tool.name}
        onCheckedChange={onChange}
      />
      <div className="min-w-0 flex-1">
        <Label htmlFor={id} className="block truncate font-mono text-xs">
          {label}
        </Label>
        <p className="text-muted-foreground truncate text-xs" title={tool.description}>
          {toolSummary(tool.description)}
        </p>
      </div>
      <span
        className="text-muted-foreground shrink-0 font-mono text-2xs tabular-nums"
        title="Estimated tokens its definition costs every request"
      >
        ~{formatTokens(tool.tokens)}
      </span>
    </li>
  );
}
