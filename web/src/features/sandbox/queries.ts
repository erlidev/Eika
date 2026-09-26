/** Server state for a workspace's sandbox: its usage, changing it, and opening a preview. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { getWorkspaceUsage, openPreview, setWorkspaceSandbox } from "@/api/routes";
import type { PreviewLink, Sandbox, Workspace, WorkspaceUsage } from "@/api/types";

/**
 * usageInterval is how often the Sandbox panel samples a running workspace.
 * A sample is a measurement no event announces, so it is the one thing the
 * UI asks for again on a timer, and only while the panel shows it.
 */
const usageInterval = 5_000;

/** useWorkspaceUsage samples what a running workspace consumes, every few seconds. */
export function useWorkspaceUsage(
  workspaceId: string,
  running: boolean,
): UseQueryResult<WorkspaceUsage> {
  return useQuery({
    queryKey: queryKeys.workspaceUsage(workspaceId),
    queryFn: ({ signal }) => getWorkspaceUsage(workspaceId, signal),
    enabled: running,
    refetchInterval: running ? usageInterval : false,
    // A sample goes stale at once; showing the last one while the next is
    // taken is what keeps the meters from flickering.
    placeholderData: (previous) => previous,
  });
}

/** useSetSandbox changes a workspace's limits, network, and ports. */
export function useSetSandbox(): UseMutationResult<
  Workspace,
  Error,
  { workspaceId: string; sandbox: Sandbox }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ workspaceId, sandbox }) => setWorkspaceSandbox(workspaceId, sandbox),
    onSuccess: async (workspace) => {
      client.setQueryData(queryKeys.workspace(workspace.id), workspace);
      await client.invalidateQueries({ queryKey: ["workspaces"] });
      await client.invalidateQueries({ queryKey: queryKeys.workspaceUsage(workspace.id) });
    },
  });
}

/**
 * useOpenPreview asks for a preview link and opens it in a new tab. The tab
 * is opened before the request, while the click still counts as the user's,
 * so a popup blocker lets it through; it is closed again if the request
 * fails.
 */
export function useOpenPreview(): UseMutationResult<
  PreviewLink,
  Error,
  { workspaceId: string; port: number }
> {
  return useMutation({
    mutationFn: async ({ workspaceId, port }) => {
      const tab = window.open("", "_blank");
      try {
        const link = await openPreview(workspaceId, port);
        if (tab !== null) {
          tab.opener = null;
          tab.location.href = link.url;
        }
        return link;
      } catch (err) {
        tab?.close();
        throw err;
      }
    },
  });
}
