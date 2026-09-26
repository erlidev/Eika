/**
 * The tools of a request: the largest definitions, and every tool with its
 * parameters or its raw schema.
 */

import { useState } from "react";

import type { ToolSchema } from "@/api/types";
import { OutputBlock } from "@/components/OutputBlock";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Dot } from "@/features/context/parts";
import { schemaParameters } from "@/features/context/segments";
import type { ToolGroup, View } from "@/features/context/segments";
import { formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";

type ToolSizesProps = { groups: readonly ToolGroup[]; onView: (view: View) => void };

/** ToolSizes ranks the tools by what their definitions cost, the largest first. */
export function ToolSizes({ groups, onView }: ToolSizesProps) {
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

type ToolListProps = { groups: readonly ToolGroup[] };

/** ToolList is every tool of the groups, with a search over them. */
export function ToolList({ groups }: ToolListProps) {
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

type ToolCardProps = { tool: ToolSchema; server: string | undefined };

/** ToolCard is one tool definition: its name, description, and parameters, or its raw schema. */
function ToolCard({ tool, server }: ToolCardProps) {
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
