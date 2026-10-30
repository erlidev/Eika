/**
 * Where an MCP server's connection stands: for an http server, connecting
 * and signing in; for a stdio server, the sandboxes it runs in.
 */

import type { MCPServer, MCPServerDetails } from "@/api/types";
import { ActionError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import {
  useAuthorizeMCPServer,
  useConnectMCPServer,
  useSignOutMCPServer,
} from "@/features/mcp/queries";

type RemoteConnectionProps = { details: MCPServerDetails };

/** RemoteConnection is where an http server's connection and sign-in stand, with what changes them. */
export function RemoteConnection({ details }: RemoteConnectionProps) {
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

type StdioConnectionProps = { server: MCPServer };

/** StdioConnection is where a stdio server runs. A run starts it; there is nothing to start by hand. */
export function StdioConnection({ server }: StdioConnectionProps) {
  const runsIn = server.workspaces ?? [];
  return (
    <div className="space-y-2">
      <StateNotice server={server} />
      <p className="text-muted-foreground text-xs">
        {runsIn.length === 0
          ? "Not running. A chat that uses it starts it in its sandbox."
          : `Running in ${String(runsIn.length)} ${runsIn.length === 1 ? "sandbox" : "sandboxes"}.`}
      </p>
    </div>
  );
}

type StateNoticeProps = { server: MCPServer };

/** StateNotice says why a server is not connected, when there is something to say. */
function StateNotice({ server }: StateNoticeProps) {
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
