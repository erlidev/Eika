/**
 * One MCP server's page in the settings: where its connection stands and
 * why, the actions that change it (connect, sign in and out, turn it off,
 * edit, delete), and what it serves: its tools, each of which can be left
 * out of every run, its resources and prompts, which can be tried here, its
 * log, and what the connection learned of it.
 */

import { ChevronLeft, Pencil, Trash2 } from "lucide-react";
import { useState } from "react";

import { ApiError } from "@/api/client";
import type { MCPServerDetails } from "@/api/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { SectionTabs } from "@/components/SectionTabs";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { location } from "@/features/mcp/describe";
import { AboutTab } from "@/features/mcp/MCPServerAbout";
import { RemoteConnection, StdioConnection } from "@/features/mcp/MCPServerConnection";
import { MCPServerForm } from "@/features/mcp/MCPServerForm";
import { PromptsTab, ResourcesTab, ToolsTab } from "@/features/mcp/MCPServerTabs";
import { MCPStateBadge } from "@/features/mcp/MCPStateBadge";
import { useDeleteMCPServer, useMCPServer, useUpdateMCPServer } from "@/features/mcp/queries";
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
