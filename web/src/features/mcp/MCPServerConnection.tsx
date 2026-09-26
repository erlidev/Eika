/**
 * Where an MCP server's connection stands: for an http server, connecting
 * and signing in; for a stdio server, the workspaces it runs in and a way to
 * start it in one.
 */

import type { MCPServer, MCPServerDetails } from "@/api/types";
import { ActionError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  useAuthorizeMCPServer,
  useConnectMCPServer,
  useSignOutMCPServer,
} from "@/features/mcp/queries";
import { useWorkspaces } from "@/features/workspaces";

/** RemoteConnection is where an http server's connection and sign-in stand, with what changes them. */
export function RemoteConnection({ details }: { details: MCPServerDetails }) {
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
export function StdioConnection({ server, workspaceId, onWorkspace }: StdioConnectionProps) {
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
