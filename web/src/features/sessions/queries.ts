/** Server state for the session list of a workspace. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { createSession, deleteSession, listSessions } from "@/api/routes";
import type { Session } from "@/api/types";

/**
 * useSessions lists the sessions of one workspace, or all of them.
 * `descendants` also brings back the forks and child agents they led to,
 * which the sidebar hangs under the session each came from.
 */
export function useSessions(
  workspaceId?: string,
  descendants?: boolean,
): UseQueryResult<Session[]> {
  return useQuery({
    queryKey: queryKeys.sessions(workspaceId, descendants),
    queryFn: ({ signal }) => listSessions(workspaceId, descendants, signal),
  });
}

/** useCreateSession opens a session in a workspace. */
export function useCreateSession(): UseMutationResult<
  Session,
  Error,
  { workspace_id: string; title: string }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: createSession,
    onSuccess: () => client.invalidateQueries({ queryKey: ["sessions"] }),
  });
}

/** useDeleteSession aborts a session's run and deletes it. */
export function useDeleteSession(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: deleteSession,
    onSuccess: () => client.invalidateQueries({ queryKey: ["sessions"] }),
  });
}
