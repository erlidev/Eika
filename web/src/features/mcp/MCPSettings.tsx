/**
 * The MCP tab of the settings: every configured server with the state its
 * connection is in, a way to add one, and each server's own page. The
 * `mcp.server` event keeps it current, so nothing here polls.
 */

import { ChevronRight, Plus } from "lucide-react";
import { useState } from "react";

import type { MCPServer } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { location } from "@/features/mcp/describe";
import { MCPServerForm } from "@/features/mcp/MCPServerForm";
import { MCPServerView } from "@/features/mcp/MCPServerView";
import { useMCPServers } from "@/features/mcp/queries";
import { MCPStateBadge } from "@/features/mcp/MCPStateBadge";
import { useMCPSelection } from "@/features/mcp/store";

export function MCPSettings() {
  const selected = useMCPSelection((s) => s.serverId);
  const select = useMCPSelection((s) => s.select);
  if (selected !== "") {
    return (
      <MCPServerView
        key={selected}
        id={selected}
        onBack={() => {
          select("");
        }}
      />
    );
  }
  return <ServerList onOpen={select} />;
}

function ServerList({ onOpen }: { onOpen: (id: string) => void }) {
  const servers = useMCPServers();
  const [adding, setAdding] = useState(false);
  const list = servers.data ?? [];

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <p className="text-muted-foreground text-sm">
          Model Context Protocol servers give agents tools, resources, and prompts of other
          services. A run offers the tools of the servers that are on, beside the built-in ones.
        </p>
        <Button
          size="sm"
          onClick={() => {
            setAdding(true);
          }}
        >
          <Plus aria-hidden />
          Add server
        </Button>
      </div>

      {servers.isPending && <Notice tone="pending">Loading the MCP servers…</Notice>}
      {servers.isError && (
        <LoadError
          what="the MCP servers"
          error={servers.error}
          retrying={servers.isFetching}
          retry={() => void servers.refetch()}
        />
      )}
      {servers.isSuccess && list.length === 0 && (
        <div className="rounded-md border border-dashed p-8 text-center">
          <p className="text-sm font-medium">No MCP servers yet</p>
          <p className="text-muted-foreground mt-1 text-sm">
            Add a remote server by its URL, or a command that runs one in each workspace.
          </p>
        </div>
      )}

      {list.length > 0 && (
        <ul className="divide-y overflow-hidden rounded-md border" aria-label="MCP servers">
          {list.map((server) => (
            <ServerRow
              key={server.id}
              server={server}
              onOpen={() => {
                onOpen(server.id);
              }}
            />
          ))}
        </ul>
      )}

      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Add an MCP server</DialogTitle>
            <DialogDescription>
              A remote server connects as soon as it is added; a stdio one starts in the workspace
              of the first session that uses it.
            </DialogDescription>
          </DialogHeader>
          <MCPServerForm
            onCancel={() => {
              setAdding(false);
            }}
            onSaved={(server) => {
              setAdding(false);
              onOpen(server.id);
            }}
          />
        </DialogContent>
      </Dialog>
    </div>
  );
}

function ServerRow({ server, onOpen }: { server: MCPServer; onOpen: () => void }) {
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        aria-label={`${server.name}, ${server.kind}`}
        aria-describedby={`mcp-row-${server.id}-state`}
        className="hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors focus-visible:ring-1 focus-visible:outline-none focus-visible:ring-inset"
      >
        <div className="min-w-40 flex-1">
          <p className="flex min-w-0 items-center gap-2">
            <span className="truncate font-mono text-sm font-medium">{server.name}</span>
            <Badge variant="outline" className="h-4 shrink-0 px-1 font-mono text-2xs">
              {server.kind}
            </Badge>
            <span id={`mcp-row-${server.id}-state`} className="contents">
              <MCPStateBadge state={server.state} />
              {server.error !== undefined && server.error !== "" && (
                <span className="sr-only">: {server.error}</span>
              )}
            </span>
          </p>
          <p className="text-muted-foreground truncate font-mono text-xs">{location(server)}</p>
          {server.error !== undefined && server.error !== "" && (
            <p className="text-muted-foreground truncate text-xs">{server.error}</p>
          )}
        </div>
        <ChevronRight aria-hidden className="text-muted-foreground size-4 shrink-0" />
      </button>
    </li>
  );
}
