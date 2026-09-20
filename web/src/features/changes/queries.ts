/** Server state for a workspace's changes: its diff, and the commit and push actions. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { commitWorkspace, getWorkspaceDiff, pushWorkspace } from "@/api/routes";
import type {
  CommitRequest,
  CommitResult,
  PushRequest,
  PushResult,
  WorkspaceDiff,
} from "@/api/types";

/**
 * useWorkspaceDiff reads the workspace's changes. The `workspace.state` event
 * refreshes it (its key sits under the workspace's), and so does a save.
 */
export function useWorkspaceDiff(workspaceId: string): UseQueryResult<WorkspaceDiff> {
  return useQuery({
    queryKey: queryKeys.workspaceDiff(workspaceId),
    queryFn: ({ signal }) => getWorkspaceDiff(workspaceId, signal),
  });
}

/** useCommit commits the workspace's changes. */
export function useCommit(
  workspaceId: string,
): UseMutationResult<CommitResult, Error, CommitRequest> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input) => commitWorkspace(workspaceId, input),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.workspaceDiff(workspaceId) }),
  });
}

/** usePush pushes the workspace's branch to the hub, and upstream when asked. */
export function usePush(workspaceId: string): UseMutationResult<PushResult, Error, PushRequest> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input) => pushWorkspace(workspaceId, input),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.workspaceDiff(workspaceId) }),
  });
}
