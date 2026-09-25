/** Server state for MCP servers, their authorization, and what they serve. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient, UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { globalTopic, payloadOf } from "@/api/events";
import { queryKeys } from "@/api/keys";
import {
  authorizeMCPServer,
  connectMCPServer,
  createMCPServer,
  deleteMCPServer,
  finishMCPAuthorization,
  getMCPPrompt,
  getMCPServer,
  listMCPServers,
  readMCPResource,
  signOutMCPServer,
  updateMCPServer,
} from "@/api/routes";
import type {
  ContentDetail,
  CreateMCPServer,
  MCPPromptResult,
  MCPServer,
  MCPServerDetails,
  OAuthCallback,
  UpdateMCPServer,
} from "@/api/types";
import { useStreamSubscription } from "@/api/useStream";

/**
 * refresh invalidates what a change of a server makes stale: the servers,
 * and the tool list, which holds what each server offers.
 */
async function refresh(client: QueryClient): Promise<void> {
  await Promise.all([
    client.invalidateQueries({ queryKey: queryKeys.mcpServers() }),
    client.invalidateQueries({ queryKey: queryKeys.tools() }),
  ]);
}

/** useMCPServers lists the configured MCP servers by name. */
export function useMCPServers(): UseQueryResult<MCPServer[]> {
  return useQuery({
    queryKey: [...queryKeys.mcpServers(), "list"],
    queryFn: ({ signal }) => listMCPServers(signal),
  });
}

/** useMCPServer reads everything the harness knows of one server. */
export function useMCPServer(id: string): UseQueryResult<MCPServerDetails> {
  return useQuery({
    queryKey: queryKeys.mcpServer(id),
    queryFn: ({ signal }) => getMCPServer(id, signal),
  });
}

/**
 * useMCPEvents keeps every MCP query current: an `mcp.server` event on the
 * global topic says a server's state, what it serves, or its configuration
 * changed. The workbench calls it once, so the settings and a chat's tools
 * never poll.
 */
export function useMCPEvents(): void {
  const client = useQueryClient();
  useStreamSubscription(globalTopic, (e) => {
    if (!payloadOf(e, "mcp.server")) return;
    void refresh(client);
  });
}

/** useCreateMCPServer configures a server. */
export function useCreateMCPServer(): UseMutationResult<MCPServer, Error, CreateMCPServer> {
  const client = useQueryClient();
  return useMutation({ mutationFn: createMCPServer, onSuccess: () => refresh(client) });
}

/** useUpdateMCPServer changes a server: its configuration, whether it is on, its tools. */
export function useUpdateMCPServer(): UseMutationResult<
  MCPServer,
  Error,
  { id: string; input: UpdateMCPServer }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }) => updateMCPServer(id, input),
    onSuccess: () => refresh(client),
  });
}

/** useDeleteMCPServer removes a server. */
export function useDeleteMCPServer(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: deleteMCPServer,
    onSuccess: async (_, id) => {
      client.removeQueries({ queryKey: queryKeys.mcpServer(id) });
      await refresh(client);
    },
  });
}

/** useConnectMCPServer connects a server again, in a workspace for a stdio one. */
export function useConnectMCPServer(): UseMutationResult<
  MCPServerDetails,
  Error,
  { id: string; workspaceId?: string }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, workspaceId }) => connectMCPServer(id, workspaceId),
    onSuccess: async (details) => {
      client.setQueryData(queryKeys.mcpServer(details.server.id), details);
      await refresh(client);
    },
  });
}

/** callbackPath is the web UI's page an authorization server sends the browser back to. */
export const callbackPath = "/mcp/callback";

/**
 * useAuthorizeMCPServer starts an authorization and sends the browser to the
 * authorization server, which sends it back to the callback page.
 */
export function useAuthorizeMCPServer(): UseMutationResult<string, Error, string> {
  return useMutation({
    mutationFn: (id: string) => authorizeMCPServer(id, `${window.location.origin}${callbackPath}`),
    onSuccess: (url) => {
      window.location.assign(url);
    },
  });
}

/** useFinishMCPAuthorization hands the authorization server's answer to the harness. */
export function useFinishMCPAuthorization(): UseMutationResult<string, Error, OAuthCallback> {
  const client = useQueryClient();
  return useMutation({ mutationFn: finishMCPAuthorization, onSuccess: () => refresh(client) });
}

/** useSignOutMCPServer revokes and forgets a server's tokens. */
export function useSignOutMCPServer(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({ mutationFn: signOutMCPServer, onSuccess: () => refresh(client) });
}

/** useReadMCPResource reads one resource when the user asks. */
export function useReadMCPResource(): UseMutationResult<
  ContentDetail[],
  Error,
  { id: string; uri: string; workspaceId?: string }
> {
  return useMutation({
    mutationFn: ({ id, uri, workspaceId }) => readMCPResource(id, uri, workspaceId),
  });
}

/** useGetMCPPrompt renders one prompt when the user asks. */
export function useGetMCPPrompt(): UseMutationResult<
  MCPPromptResult,
  Error,
  { id: string; name: string; args: Record<string, string>; workspaceId?: string }
> {
  return useMutation({
    mutationFn: ({ id, name, args, workspaceId }) => getMCPPrompt(id, name, args, workspaceId),
  });
}
