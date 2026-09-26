/**
 * What an MCP server serves, a tab each: its tools, each of which can be left
 * out of every run, and its resources and prompts, which can be tried here.
 */

import { ChevronRight } from "lucide-react";
import { useState } from "react";

import type { MCPPrompt, MCPServerDetails, MCPTool } from "@/api/types";
import { ActionError, Notice } from "@/components/Notice";
import { OutputBlock } from "@/components/OutputBlock";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { ContentBlocks } from "@/features/mcp/ContentBlocks";
import { toolFlags, withDisabled } from "@/features/mcp/describe";
import { useGetMCPPrompt, useReadMCPResource, useUpdateMCPServer } from "@/features/mcp/queries";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";

type ListErrorProps = { details: MCPServerDetails; method: string };

/** ListError is a list the server could not give, when it could not. */
function ListError({ details, method }: ListErrorProps) {
  const error = details.list_errors?.[method];
  if (error === undefined) return null;
  return <Notice tone="error">{`The server did not list them (${method}): ${error}`}</Notice>;
}

type NotListedProps = { details: MCPServerDetails; what: string };

/** NotListed is what an empty tab says before anything was listed. */
function NotListed({ details, what }: NotListedProps) {
  return (
    <p className="text-muted-foreground text-xs">
      {details.fetched_at === undefined
        ? `Nothing listed yet: connect the server to see its ${what}.`
        : `The server lists no ${what}.`}
    </p>
  );
}

type ToolsTabProps = { details: MCPServerDetails };

export function ToolsTab({ details }: ToolsTabProps) {
  const server = details.server;
  const update = useUpdateMCPServer();
  const tools = details.tools ?? [];
  const excluded = details.excluded_tools ?? [];
  // While a change is on its way the switches show it.
  const disabled = update.isPending
    ? (update.variables.input.disabled_tools ?? server.disabled_tools ?? [])
    : (server.disabled_tools ?? []);
  return (
    <div className="space-y-3">
      <ListError details={details} method="tools/list" />
      {tools.length === 0 && excluded.length === 0 ? (
        <NotListed details={details} what="tools" />
      ) : (
        <p className="text-muted-foreground text-xs">
          A tool that is off is left out of every run. The labels are the server&apos;s own claims
          about a tool, not guarantees.
        </p>
      )}
      <ul className="space-y-1.5" aria-label={`${server.name} tools`}>
        {tools.map((tool) => (
          <ToolRow
            key={tool.name}
            tool={tool}
            on={!disabled.includes(tool.name)}
            busy={update.isPending}
            onChange={(on) => {
              update.mutate({
                id: server.id,
                input: {
                  disabled_tools: withDisabled(
                    { ...server, disabled_tools: disabled },
                    tool.name,
                    on,
                  ),
                },
              });
            }}
          />
        ))}
      </ul>
      {update.isError && <ActionError action="change the tools" error={update.error} />}
      {excluded.length > 0 && (
        <section className="space-y-1">
          <h4 className="text-muted-foreground text-2xs font-semibold tracking-wide uppercase">
            Never offered
          </h4>
          <ul className="space-y-1 text-xs">
            {excluded.map((t) => (
              <li key={t.name}>
                <span className="font-mono">{t.name}</span>
                <span className="text-muted-foreground">: {t.reason}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

type ToolRowProps = {
  tool: MCPTool;
  on: boolean;
  busy: boolean;
  onChange: (on: boolean) => void;
};

function ToolRow({ tool, on, busy, onChange }: ToolRowProps) {
  const id = `mcp-tool-${tool.name}`;
  const flags = toolFlags(tool.annotations);
  const title = tool.title ?? tool.annotations?.title;
  return (
    <li className="rounded-md border">
      <div className="flex items-start gap-3 p-2">
        <div className="min-w-0 flex-1 space-y-0.5">
          <div className="flex flex-wrap items-center gap-1.5">
            <Label htmlFor={id} className="font-mono text-xs">
              {tool.name}
            </Label>
            {flags.map((flag) => (
              <Badge
                key={flag}
                variant="outline"
                className={cn(
                  "h-4 px-1 text-2xs",
                  flag === "destructive" && "border-destructive/40 text-destructive",
                )}
              >
                {flag}
              </Badge>
            ))}
          </div>
          {title !== undefined && title !== tool.name && <p className="text-xs">{title}</p>}
          {tool.description !== undefined && (
            <p className="text-muted-foreground line-clamp-2 text-xs">{tool.description}</p>
          )}
        </div>
        <Switch id={id} checked={on} disabled={busy} onCheckedChange={onChange} />
      </div>
      <Collapsible className="border-t">
        <CollapsibleTrigger className="group hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-1.5 px-2 py-1 text-left text-xs transition-colors focus-visible:ring-1 focus-visible:outline-none">
          <ChevronRight
            aria-hidden
            className="size-3 shrink-0 transition-transform group-data-[state=open]:rotate-90"
          />
          <span className="text-muted-foreground">
            Schema and full description · the model calls it{" "}
            <span className="font-mono">{tool.exposed_name}</span>
          </span>
        </CollapsibleTrigger>
        <CollapsibleContent className="space-y-2 p-2 pt-0">
          {tool.description !== undefined && (
            <p className="text-muted-foreground text-xs whitespace-pre-wrap">{tool.description}</p>
          )}
          <OutputBlock label={`${tool.name} input schema`} maxHeightClass="max-h-60">
            {JSON.stringify(tool.input_schema, null, 2)}
          </OutputBlock>
          {tool.output_schema !== undefined && (
            <OutputBlock label={`${tool.name} output schema`} maxHeightClass="max-h-60">
              {JSON.stringify(tool.output_schema, null, 2)}
            </OutputBlock>
          )}
        </CollapsibleContent>
      </Collapsible>
    </li>
  );
}

type ServesProps = {
  details: MCPServerDetails;
  /** workspaceId is where a stdio server is asked; undefined for an http one. */
  workspaceId: string | undefined;
};

export function ResourcesTab({ details, workspaceId }: ServesProps) {
  const server = details.server;
  const read = useReadMCPResource();
  const resources = details.resources ?? [];
  const templates = details.resource_templates ?? [];
  const needsWorkspace = server.kind === "stdio" && workspaceId === undefined;
  return (
    <div className="space-y-3">
      <ListError details={details} method="resources/list" />
      {resources.length === 0 && templates.length === 0 && (
        <NotListed details={details} what="resources" />
      )}
      {resources.length > 0 && (
        <ul className="divide-y rounded-md border" aria-label={`${server.name} resources`}>
          {resources.map((r) => {
            const shown = read.variables?.uri === r.uri;
            return (
              <li key={r.uri} className="space-y-2 p-2">
                <div className="flex flex-wrap items-start gap-x-3 gap-y-1">
                  <div className="min-w-40 flex-1">
                    <p className="truncate text-xs font-medium">{r.title ?? r.name}</p>
                    <p className="text-muted-foreground truncate font-mono text-xs">
                      {r.uri}
                      {r.mime_type !== undefined && ` · ${r.mime_type}`}
                      {r.size !== undefined && ` · ${formatBytes(r.size)}`}
                    </p>
                    {r.description !== undefined && (
                      <p className="text-muted-foreground text-xs">{r.description}</p>
                    )}
                  </div>
                  <Button
                    size="xs"
                    variant="outline"
                    aria-label={`Read ${r.title ?? r.name}`}
                    disabled={read.isPending || needsWorkspace}
                    onClick={() => {
                      read.mutate({
                        id: server.id,
                        uri: r.uri,
                        ...(workspaceId === undefined ? {} : { workspaceId }),
                      });
                    }}
                  >
                    {read.isPending && shown ? "Reading…" : "Read"}
                  </Button>
                </div>
                {shown && read.isSuccess && <ContentBlocks blocks={read.data} />}
                {shown && read.isError && (
                  <ActionError action={`read ${r.uri}`} error={read.error} />
                )}
              </li>
            );
          })}
        </ul>
      )}
      {templates.length > 0 && (
        <section className="space-y-1">
          <h4 className="text-muted-foreground text-2xs font-semibold tracking-wide uppercase">
            Templates
          </h4>
          <ul className="space-y-1 text-xs">
            {templates.map((t) => (
              <li key={t.uri_template}>
                <p>
                  <span className="font-medium">{t.title ?? t.name}</span>
                  {t.description !== undefined && (
                    <span className="text-muted-foreground">: {t.description}</span>
                  )}
                </p>
                <p className="text-muted-foreground font-mono">{t.uri_template}</p>
              </li>
            ))}
          </ul>
        </section>
      )}
      {needsWorkspace && resources.length > 0 && (
        <p className="text-muted-foreground text-xs">Choose a workspace above to read them.</p>
      )}
    </div>
  );
}

export function PromptsTab({ details, workspaceId }: ServesProps) {
  const prompts = details.prompts ?? [];
  const needsWorkspace = details.server.kind === "stdio" && workspaceId === undefined;
  return (
    <div className="space-y-3">
      <ListError details={details} method="prompts/list" />
      {prompts.length === 0 && <NotListed details={details} what="prompts" />}
      <ul className="space-y-1.5" aria-label={`${details.server.name} prompts`}>
        {prompts.map((prompt) => (
          <PromptRow
            key={prompt.name}
            serverId={details.server.id}
            prompt={prompt}
            workspaceId={workspaceId}
            disabled={needsWorkspace}
          />
        ))}
      </ul>
      {needsWorkspace && prompts.length > 0 && (
        <p className="text-muted-foreground text-xs">Choose a workspace above to try them.</p>
      )}
    </div>
  );
}

type PromptRowProps = {
  serverId: string;
  prompt: MCPPrompt;
  workspaceId: string | undefined;
  disabled: boolean;
};

/** PromptRow is one prompt, with a form to render it with arguments. */
function PromptRow({ serverId, prompt, workspaceId, disabled }: PromptRowProps) {
  const get = useGetMCPPrompt();
  const [values, setValues] = useState<Record<string, string>>({});
  const args = prompt.arguments ?? [];
  const missing = args.some((a) => a.required && (values[a.name] ?? "").trim() === "");
  return (
    <li className="rounded-md border">
      <Collapsible>
        <CollapsibleTrigger className="group hover:bg-accent focus-visible:ring-ring flex w-full items-start gap-2 p-2 text-left transition-colors focus-visible:ring-1 focus-visible:outline-none">
          <ChevronRight
            aria-hidden
            className="mt-0.5 size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-90"
          />
          <span className="min-w-0 space-y-0.5">
            <span className="block font-mono text-xs font-medium">{prompt.name}</span>
            {(prompt.title ?? prompt.description) !== undefined && (
              <span className="text-muted-foreground block text-xs">
                {prompt.title ?? prompt.description}
              </span>
            )}
          </span>
        </CollapsibleTrigger>
        <CollapsibleContent className="space-y-2 border-t p-2">
          {prompt.title !== undefined && prompt.description !== undefined && (
            <p className="text-muted-foreground text-xs">{prompt.description}</p>
          )}
          <form
            className="space-y-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (missing || disabled) return;
              get.mutate({
                id: serverId,
                name: prompt.name,
                args: Object.fromEntries(Object.entries(values).filter(([, v]) => v !== "")),
                ...(workspaceId === undefined ? {} : { workspaceId }),
              });
            }}
          >
            {args.map((a) => {
              const id = `mcp-prompt-${prompt.name}-${a.name}`;
              return (
                <div key={a.name} className="space-y-1">
                  <Label htmlFor={id} className="text-xs">
                    <span className="font-mono">{a.name}</span>
                    {a.required ? "" : " (optional)"}
                  </Label>
                  <Input
                    id={id}
                    value={values[a.name] ?? ""}
                    autoComplete="off"
                    className="h-8 text-xs"
                    placeholder={a.description ?? a.title ?? ""}
                    required={a.required}
                    onChange={(e) => {
                      setValues((v) => ({ ...v, [a.name]: e.target.value }));
                    }}
                  />
                </div>
              );
            })}
            <Button type="submit" size="xs" disabled={missing || disabled || get.isPending}>
              {get.isPending ? "Getting…" : "Get the prompt"}
            </Button>
          </form>
          {get.isError && <ActionError action={`get ${prompt.name}`} error={get.error} />}
          {get.isSuccess && (
            <ol className="space-y-2" aria-label={`${prompt.name} messages`}>
              {(get.data.messages ?? []).map((m, i) => (
                <li key={i} className="space-y-1">
                  <p className="text-muted-foreground text-2xs font-semibold tracking-wide uppercase">
                    {m.role}
                  </p>
                  <ContentBlocks blocks={[m.content]} />
                </li>
              ))}
            </ol>
          )}
        </CollapsibleContent>
      </Collapsible>
    </li>
  );
}
