/** Server state for workspaces, including their lifecycle actions. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import {
  createWorkspace,
  deleteWorkspace,
  getWorkspace,
  listWorkspaces,
  startWorkspace,
  stopWorkspace,
} from "@/api/routes";
import type { CreateWorkspace, Workspace } from "@/api/types";

/** useWorkspaces lists the workspaces of one project, or all of them. */
export function useWorkspaces(projectId?: string): UseQueryResult<Workspace[]> {
  return useQuery({
    queryKey: queryKeys.workspaces(projectId),
    queryFn: ({ signal }) => listWorkspaces(projectId, signal),
  });
}

/** useWorkspace reads one workspace, which is how a session finds its sandbox. */
export function useWorkspace(id: string | undefined): UseQueryResult<Workspace> {
  return useQuery({
    queryKey: queryKeys.workspace(id ?? ""),
    queryFn: ({ signal }) => getWorkspace(id ?? "", signal),
    enabled: id !== undefined && id !== "",
  });
}

/** useCreateWorkspace creates and starts a sandbox for a project. */
export function useCreateWorkspace(): UseMutationResult<Workspace, Error, CreateWorkspace> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: createWorkspace,
    onSuccess: () => client.invalidateQueries({ queryKey: ["workspaces"] }),
  });
}

/** WorkspaceAction is a lifecycle transition the sidebar offers. */
export type WorkspaceAction = "start" | "stop";

/** useWorkspaceAction starts or stops a workspace's container. */
export function useWorkspaceAction(): UseMutationResult<
  Workspace,
  Error,
  { id: string; action: WorkspaceAction }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, action }) => (action === "start" ? startWorkspace(id) : stopWorkspace(id)),
    // The workspace.state event refreshes the list too, but an action taken
    // here must not wait for the stream to come back.
    onSuccess: async (workspace) => {
      client.setQueryData(queryKeys.workspace(workspace.id), workspace);
      await client.invalidateQueries({ queryKey: ["workspaces"] });
    },
  });
}

/** useDeleteWorkspace destroys a workspace with its container and sessions. */
export function useDeleteWorkspace(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: deleteWorkspace,
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["workspaces"] });
      await client.invalidateQueries({ queryKey: ["sessions"] });
    },
  });
}
