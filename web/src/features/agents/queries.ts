/** Server state for the agents: the built-ins with the user's class and prompt overrides. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient, UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { listAgents, putAgent, resetAgent } from "@/api/routes";
import type { Agent, AgentOverride } from "@/api/types";

/**
 * refresh invalidates what an override changes beyond the agents: every
 * session's configuration, the preview of its next request, and its status.
 */
async function refresh(client: QueryClient): Promise<void> {
  await Promise.all([
    client.invalidateQueries({ queryKey: queryKeys.agents() }),
    client.invalidateQueries({ queryKey: ["session"] }),
    client.invalidateQueries({ queryKey: ["sessions"] }),
  ]);
}

/** useAgents lists the built-in agents, with their effective values and overrides. */
export function useAgents(): UseQueryResult<Agent[]> {
  return useQuery({
    queryKey: queryKeys.agents(),
    queryFn: ({ signal }) => listAgents(signal),
  });
}

/** useSaveAgent replaces an agent's class and prompt overrides. */
export function useSaveAgent(): UseMutationResult<
  Agent,
  Error,
  { name: string; override: AgentOverride }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ name, override }) => putAgent(name, override),
    onSuccess: () => refresh(client),
  });
}

/** useResetAgent removes an agent's overrides, so it runs as built. */
export function useResetAgent(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({ mutationFn: resetAgent, onSuccess: () => refresh(client) });
}
