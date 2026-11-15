/** Server state for Activity: the subagents, the projects they belong to, and cancelling one. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { abortRun, listProjects, listSessions } from "@/api/routes";
import type { Project, Run, Session } from "@/api/types";
import { isSubagent } from "@/features/activity/state";

/**
 * useSubagents lists Eika's subagents. The harness lists every session, so
 * the subagents are picked out here. The list sits under the sessions prefix,
 * which `session.updated` on the global topic invalidates, so a state change
 * reaches the board without polling.
 */
export function useSubagents(): UseQueryResult<Session[]> {
  return useQuery({
    queryKey: queryKeys.allSessions(),
    queryFn: ({ signal }) => listSessions(signal),
    select: (sessions) => sessions.filter(isSubagent),
  });
}

/** useProjects lists the projects, which name what a subagent works on. */
export function useProjects(): UseQueryResult<Project[]> {
  return useQuery({
    queryKey: queryKeys.projects(),
    queryFn: ({ signal }) => listProjects(signal),
  });
}

/**
 * useCancelSubagent stops a subagent's run, which the harness records as the
 * subagent cancelled. The stream says so too; the refetch does not wait for it.
 */
export function useCancelSubagent(sessionId: string): UseMutationResult<Run, Error, string> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: abortRun,
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["sessions"] });
      await client.invalidateQueries({ queryKey: queryKeys.session(sessionId) });
    },
  });
}
