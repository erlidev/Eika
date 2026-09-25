/**
 * One MCP server's page in the settings: where its connection stands and
 * why, the actions that change it (connect, sign in and out, turn it off,
 * edit, delete), and what it serves: its tools, each of which can be left
 * out of every run, its resources and prompts, which can be tried here, its
 * log, and what the connection learned of it.
 */

import { ChevronLeft, ChevronRight, Pencil, Trash2 } from "lucide-react";
import { useState } from "react";

import { ApiError } from "@/api/client";
import type { MCPPrompt, MCPServer, MCPServerDetails, MCPTool } from "@/api/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { OutputBlock } from "@/components/OutputBlock";
import { SectionTabs } from "@/components/SectionTabs";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { ContentBlocks } from "@/features/mcp/ContentBlocks";
import {
  eraText,
  location,
  toolFlags,
  transportLabels,
  withDisabled,
} from "@/features/mcp/describe";
import { MCPServerForm } from "@/features/mcp/MCPServerForm";
import { MCPStateBadge } from "@/features/mcp/MCPStateBadge";
import {
  useAuthorizeMCPServer,
  useConnectMCPServer,
  useDeleteMCPServer,
  useGetMCPPrompt,
  useMCPServer,
  useReadMCPResource,
  useSignOutMCPServer,
  useUpdateMCPServer,
} from "@/features/mcp/queries";
import { useWorkspaces } from "@/features/workspaces";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";

export type MCPServerViewProps = {
  id: string;
  /** onBack returns to the list of servers. */
  onBack: () => void;
};

export function MCPServerView({ id, onBack }: MCPServerViewProps) {
  const details = useMCPServer(id);
  const back = (
    <Button size="xs" variant="ghost" className="-ml-2" onClick={onBack}>
      <ChevronLeft aria-hidden />
      All servers
    </Button>
  );

  if (details.isPending) {
    return (
      <div className="space-y-3">
        {back}
        <Notice tone="pending">Loading the server…</Notice>
      </div>
    );
  }
  if (details.isError) {
    const gone = details.error instanceof ApiError && details.error.status === 404;
    return (
      <div className="space-y-3">
        {back}
        {gone ? (
          <Notice>This server was deleted.</Notice>
        ) : (
          <LoadError
            what="the server"
            error={details.error}
            retrying={details.isFetching}
            retry={() => void details.refetch()}
          />
        )}
      </div>
    );
  }
  return <ServerPage details={details.data} back={back} onDeleted={onBack} />;
}

type ServerPageProps = {
  details: MCPServerDetails;
  back: React.ReactNode;
  onDeleted: () => void;
};

function ServerPage({ details, back, onDeleted }: ServerPageProps) {
  const server = details.server;
  const update = useUpdateMCPServer();
  const remove = useDeleteMCPServer();
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [section, setSection] = useState<Section>("tools");
  // A stdio server runs in a workspace, which is where it is started and
  // where its resources and prompts are read.
  const [workspaceId, setWorkspaceId] = useState(server.workspaces?.[0] ?? "");
  const stdioWorkspace = server.kind === "stdio" && workspaceId !== "" ? workspaceId : undefined;
  const tools = details.tools ?? [];
  const resources = details.resources ?? [];
  const templates = details.resource_templates ?? [];
  const prompts = details.prompts ?? [];
  const logs = details.logs ?? [];

  return (
    <div className="space-y-4">
      {back}
      <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
        <div className="min-w-40 flex-1">
          <h3 className="flex min-w-0 items-center gap-2">
            <span className="truncate font-mono text-base font-semibold">{server.name}</span>
            <Badge variant="outline" className="h-4 shrink-0 px-1 font-mono text-2xs">
              {server.kind}
            </Badge>
            <MCPStateBadge state={server.state} />
          </h3>
          <p className="text-muted-foreground truncate font-mono text-xs">{location(server)}</p>
        </div>
        <div className="flex items-center gap-1">
          <Label htmlFor="mcp-enabled" className="text-muted-foreground mr-1 text-xs">
            On
          </Label>
          <Switch
            id="mcp-enabled"
            checked={
              update.isPending && update.variables.input.enabled !== undefined
                ? update.variables.input.enabled
                : server.enabled
            }
            disabled={update.isPending}
            onCheckedChange={(enabled) => {
              update.mutate({ id: server.id, input: { enabled } });
            }}
          />
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label={`Edit ${server.name}`}
            onClick={() => {
              setEditing(true);
            }}
          >
            <Pencil aria-hidden />
          </Button>
          <Button
            size="icon-xs"
            variant="ghost"
            aria-label={`Delete ${server.name}`}
            onClick={() => {
              setConfirming(true);
            }}
          >
            <Trash2 aria-hidden />
          </Button>
        </div>
      </div>
      {update.isError && <ActionError action={`change ${server.name}`} error={update.error} />}
      {remove.isError && <ActionError action={`delete ${server.name}`} error={remove.error} />}

      {server.kind === "http" ? (
        <RemoteConnection details={details} />
      ) : (
        <StdioConnection server={server} workspaceId={workspaceId} onWorkspace={setWorkspaceId} />
      )}

      <SectionTabs
        idPrefix="mcp"
        label="Server"
        tabs={[
          { id: "tools", title: "Tools", count: tools.length },
          { id: "resources", title: "Resources", count: resources.length + templates.length },
          { id: "prompts", title: "Prompts", count: prompts.length },
          { id: "log", title: "Log" },
          { id: "about", title: "About" },
        ]}
        value={section}
        onChange={setSection}
      />
      <div role="tabpanel" id={`mcp-section-${section}`} aria-labelledby={`mcp-tab-${section}`}>
        {section === "tools" && <ToolsTab details={details} />}
        {section === "resources" && <ResourcesTab details={details} workspaceId={stdioWorkspace} />}
        {section === "prompts" && <PromptsTab details={details} workspaceId={stdioWorkspace} />}
        {section === "log" && (
          <>
            {logs.length === 0 ? (
              <p className="text-muted-foreground text-xs">Nothing logged yet.</p>
            ) : (
              <ol
                aria-label={`${server.name} log`}
                className="bg-muted/60 max-h-80 overflow-auto rounded-md border p-2 font-mono text-xs leading-relaxed"
              >
                {logs.map((line, i) => (
                  <li key={i} className="flex gap-2 whitespace-pre-wrap">
                    <time dateTime={line.time} className="text-muted-foreground shrink-0">
                      {line.time.slice(11, 19)}
                    </time>
                    <span className="text-muted-foreground w-12 shrink-0">{line.source}</span>
                    <span
                      className={cn(
                        "min-w-0 break-words",
                        (line.level === "error" || line.level === "critical") && "text-destructive",
                        line.level === "warning" && "text-warning",
                      )}
                    >
                      {line.text}
                    </span>
                  </li>
                ))}
              </ol>
            )}
            <p className="text-muted-foreground mt-1 text-xs">
              The last 200 lines, times in UTC:{" "}
              {server.kind === "stdio" ? "the process's own output" : "what the server logged"}, and
              what the harness did.
            </p>
          </>
        )}
        {section === "about" && <AboutTab details={details} />}
      </div>

      <Dialog open={editing} onOpenChange={setEditing}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Edit {server.name}</DialogTitle>
            <DialogDescription>
              Saving ends the server&apos;s connections; the next run connects with what you save.
            </DialogDescription>
          </DialogHeader>
          {editing && (
            <MCPServerForm
              server={server}
              onCancel={() => {
                setEditing(false);
              }}
              onSaved={() => {
                setEditing(false);
              }}
            />
          )}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Delete ${server.name}?`}
        description="Its configuration, its stored values, and its sign-in are removed, and its connections end. Sessions keep the calls they made."
        confirmLabel="Delete server"
        onConfirm={() => {
          remove.mutate(server.id, { onSuccess: onDeleted });
        }}
      />
    </div>
  );
}

/** Section is one part of a server's page. */
type Section = "tools" | "resources" | "prompts" | "log" | "about";

/** RemoteConnection is where an http server's connection and sign-in stand, with what changes them. */
function RemoteConnection({ details }: { details: MCPServerDetails }) {
  const server = details.server;
  const connect = useConnectMCPServer();
  const authorize = useAuthorizeMCPServer();
  const signOut = useSignOutMCPServer();
  const auth = details.auth;
  const needsSignIn = server.state === "unauthorized";
  const signIn = (
    <Button
      size="xs"
      variant={needsSignIn ? "default" : "outline"}
      disabled={authorize.isPending || !server.enabled}
      onClick={() => {
        authorize.mutate(server.id);
      }}
    >
      {authorize.isPending ? "Opening…" : auth.authorized ? "Sign in again" : "Sign in"}
    </Button>
  );
  return (
    <div className="space-y-2">
      <StateNotice server={server} />
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="xs"
          variant="outline"
          disabled={connect.isPending || !server.enabled}
          onClick={() => {
            connect.mutate({ id: server.id });
          }}
        >
          {connect.isPending
            ? "Connecting…"
            : server.state === "connected"
              ? "Reconnect"
              : "Connect"}
        </Button>
        {(auth.challenged || auth.authorized) && signIn}
        {auth.authorized && (
          <Button
            size="xs"
            variant="ghost"
            disabled={signOut.isPending}
            onClick={() => {
              signOut.mutate(server.id);
            }}
          >
            Sign out
          </Button>
        )}
        <span className="text-muted-foreground text-xs">
          {auth.authorized
            ? `Signed in${auth.issuer === undefined ? "" : ` with ${auth.issuer}`}.`
            : auth.challenged
              ? "Sign in to let Eika use this server for you."
              : (server.header_names ?? []).length > 0
                ? `Authenticated by ${(server.header_names ?? []).join(", ")}.`
                : "No sign-in needed so far."}
        </span>
      </div>
      {connect.isError && <ActionError action={`connect ${server.name}`} error={connect.error} />}
      {authorize.isError && (
        <ActionError action={`start signing in to ${server.name}`} error={authorize.error} />
      )}
      {signOut.isError && (
        <ActionError action={`sign out of ${server.name}`} error={signOut.error} />
      )}
    </div>
  );
}

type StdioConnectionProps = {
  server: MCPServer;
  workspaceId: string;
  onWorkspace: (id: string) => void;
};

/** StdioConnection is where a stdio server runs, and a way to start it in a workspace. */
function StdioConnection({ server, workspaceId, onWorkspace }: StdioConnectionProps) {
  const workspaces = useWorkspaces();
  const connect = useConnectMCPServer();
  const running = (workspaces.data ?? []).filter((w) => w.state === "running");
  const nameOf = (id: string) => workspaces.data?.find((w) => w.id === id)?.name ?? id;
  const runsIn = server.workspaces ?? [];
  return (
    <div className="space-y-2">
      <StateNotice server={server} />
      <p className="text-muted-foreground text-xs">
        {runsIn.length === 0
          ? "Not running in any workspace. A session that uses it starts it in its own workspace; you can start it in one here to see what it offers."
          : `Running in ${runsIn.map(nameOf).join(", ")}.`}
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <Label htmlFor="mcp-workspace" className="sr-only">
          Workspace
        </Label>
        <Select value={workspaceId} onValueChange={onWorkspace} disabled={running.length === 0}>
          <SelectTrigger id="mcp-workspace" size="sm" className="w-56">
            <SelectValue
              placeholder={running.length === 0 ? "No running workspace" : "Choose a workspace"}
            />
          </SelectTrigger>
          <SelectContent>
            {running.map((w) => (
              <SelectItem key={w.id} value={w.id}>
                {w.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          size="xs"
          variant="outline"
          disabled={workspaceId === "" || connect.isPending || !server.enabled}
          onClick={() => {
            connect.mutate({ id: server.id, workspaceId });
          }}
        >
          {connect.isPending
            ? "Starting…"
            : runsIn.includes(workspaceId)
              ? "Restart there"
              : "Start there"}
        </Button>
      </div>
      {connect.isError && (
        <ActionError
          action={`start ${server.name} in ${nameOf(workspaceId)}`}
          error={connect.error}
        />
      )}
    </div>
  );
}

/** StateNotice says why a server is not connected, when there is something to say. */
function StateNotice({ server }: { server: MCPServer }) {
  switch (server.state) {
    case "disabled":
      return <Notice>Off: runs do not offer its tools, and it is not connected.</Notice>;
    case "connecting":
      return <Notice tone="pending">Connecting…</Notice>;
    case "unauthorized":
      return (
        <Notice tone="error">
          The server needs you to sign in before it answers
          {server.error !== undefined && server.error !== "" ? `: ${server.error}` : "."}
        </Notice>
      );
    case "error":
      return (
        <Notice tone="error">
          The last connection failed
          {server.error !== undefined && server.error !== "" ? `: ${server.error}` : "."} The log
          says more.
        </Notice>
      );
    default:
      return null;
  }
}

/** ListError is a list the server could not give, when it could not. */
function ListError({ details, method }: { details: MCPServerDetails; method: string }) {
  const error = details.list_errors?.[method];
  if (error === undefined) return null;
  return <Notice tone="error">{`The server did not list them (${method}): ${error}`}</Notice>;
}

/** NotListed is what an empty tab says before anything was listed. */
function NotListed({ details, what }: { details: MCPServerDetails; what: string }) {
  return (
    <p className="text-muted-foreground text-xs">
      {details.fetched_at === undefined
        ? `Nothing listed yet: connect the server to see its ${what}.`
        : `The server lists no ${what}.`}
    </p>
  );
}

function ToolsTab({ details }: { details: MCPServerDetails }) {
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

function ResourcesTab({ details, workspaceId }: ServesProps) {
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

function PromptsTab({ details, workspaceId }: ServesProps) {
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

/** Fields is a dense list of what is known, leaving out what is not. */
function Fields({ fields }: { fields: [string, React.ReactNode][] }) {
  const shown = fields.filter(([, v]) => v !== undefined && v !== null && v !== "");
  if (shown.length === 0) return null;
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
      {shown.map(([name, value]) => (
        <div key={name} className="contents">
          <dt className="text-muted-foreground">{name}</dt>
          <dd className="min-w-0 break-words">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function Mono({ children }: { children: React.ReactNode }) {
  return <span className="font-mono">{children}</span>;
}

function AboutTab({ details }: { details: MCPServerDetails }) {
  const c = details.connection;
  const auth = details.auth;
  const server = details.server;
  const capabilities = c
    ? [
        c.capabilities.tools && "tools",
        c.capabilities.tools_list_changed && "tool list updates",
        c.capabilities.resources && "resources",
        c.capabilities.resources_subscribe && "resource subscriptions",
        c.capabilities.resources_list_changed && "resource list updates",
        c.capabilities.prompts && "prompts",
        c.capabilities.prompts_list_changed && "prompt list updates",
        c.capabilities.logging && "logging",
        c.capabilities.completions && "completions",
        ...(c.capabilities.extensions ?? []),
        ...(c.capabilities.experimental ?? []).map((x) => `${x} (experimental)`),
      ].filter((x): x is string => typeof x === "string")
    : [];
  return (
    <div className="space-y-4">
      <section className="space-y-2">
        <h4 className="text-sm font-medium">Connection</h4>
        {c === undefined ? (
          <p className="text-muted-foreground text-xs">
            Not connected successfully yet, so nothing is known of the server.
          </p>
        ) : (
          <>
            <Fields
              fields={[
                ["Server", `${c.server_info.title ?? c.server_info.name} ${c.server_info.version}`],
                ["Description", c.server_info.description],
                [
                  "Website",
                  c.server_info.website_url === undefined ? undefined : (
                    <a
                      href={c.server_info.website_url}
                      target="_blank"
                      rel="noreferrer"
                      className="text-primary font-mono hover:underline"
                    >
                      {c.server_info.website_url}
                    </a>
                  ),
                ],
                ["Protocol", <Mono key="p">{eraText(c)}</Mono>],
                [
                  "Also speaks",
                  (c.supported_versions ?? []).filter((v) => v !== c.protocol_version).length >
                  0 ? (
                    <Mono key="v">
                      {(c.supported_versions ?? [])
                        .filter((v) => v !== c.protocol_version)
                        .join(", ")}
                    </Mono>
                  ) : undefined,
                ],
                ["Transport", transportLabels[c.transport]],
                ["Offers", capabilities.join(", ")],
                [
                  "Lists read",
                  details.fetched_at === undefined ? undefined : (
                    <Mono key="f">{details.fetched_at.replace("T", " ").slice(0, 19)} UTC</Mono>
                  ),
                ],
              ]}
            />
            {c.instructions !== undefined && c.instructions !== "" && (
              <div className="space-y-1">
                <p className="text-muted-foreground text-xs">
                  Instructions the server gives the model
                </p>
                <OutputBlock
                  label="server instructions"
                  maxHeightClass="max-h-40"
                  className="whitespace-pre-wrap"
                >
                  {c.instructions}
                </OutputBlock>
              </div>
            )}
          </>
        )}
      </section>
      {server.kind === "http" ? (
        <section className="space-y-2">
          <h4 className="text-sm font-medium">Authorization</h4>
          <Fields
            fields={[
              [
                "Signed in",
                auth.authorized
                  ? auth.has_refresh_token
                    ? "yes, and the token is refreshed before it expires"
                    : "yes, until the token expires"
                  : "no",
              ],
              [
                "Authorization server",
                auth.issuer === undefined ? undefined : <Mono key="i">{auth.issuer}</Mono>,
              ],
              [
                "Resource",
                auth.resource === undefined ? undefined : <Mono key="r">{auth.resource}</Mono>,
              ],
              ["Scope", auth.scope === undefined ? undefined : <Mono key="s">{auth.scope}</Mono>],
              [
                "Token expires",
                auth.expires_at === undefined ? undefined : (
                  <Mono key="e">{auth.expires_at.replace("T", " ").slice(0, 19)} UTC</Mono>
                ),
              ],
              [
                "Client",
                auth.client_id === undefined ? undefined : <Mono key="c">{auth.client_id}</Mono>,
              ],
              [
                "Registered",
                auth.registration === undefined
                  ? undefined
                  : {
                      preregistered: "by hand",
                      metadata_document: "by Eika's client metadata document",
                      dynamic: "dynamically, by Eika",
                    }[auth.registration],
              ],
              [
                "Asked for",
                auth.challenge === undefined
                  ? undefined
                  : [
                      auth.challenge.scope === undefined ? "" : `scope ${auth.challenge.scope}`,
                      auth.challenge.error_description ?? auth.challenge.error ?? "",
                    ]
                      .filter((x) => x !== "")
                      .join("; ") || "authorization",
              ],
            ]}
          />
        </section>
      ) : (
        <Notice>
          This server runs as <span className="font-mono">{location(server)}</span> in each
          workspace that uses it.
          {(server.env_names ?? []).length > 0 &&
            ` Its variables (${(server.env_names ?? []).join(", ")}) are visible to the agent there.`}
        </Notice>
      )}
    </div>
  );
}
