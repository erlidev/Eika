/** Server state for the Context panel: the next request, and the recorded ones. */

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import type { UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { getSessionContext, getSessionRequest, listSessionRequests } from "@/api/routes";
import type { ModelContext, ModelRequest } from "@/api/types";

/**
 * useNextRequest previews the session's next model request, as a run that
 * names `model` would send it. The stream invalidates it when a turn ends.
 */
export function useNextRequest(sessionId: string, model: string): UseQueryResult<ModelContext> {
  return useQuery({
    queryKey: queryKeys.sessionContext(sessionId, model),
    queryFn: ({ signal }) => getSessionContext(sessionId, model, signal),
    placeholderData: keepPreviousData,
  });
}

/** useRecordedRequests lists the session's recorded model calls, oldest first. */
export function useRecordedRequests(sessionId: string): UseQueryResult<ModelRequest[]> {
  return useQuery({
    queryKey: queryKeys.sessionRequests(sessionId),
    queryFn: ({ signal }) => listSessionRequests(sessionId, signal),
  });
}

/** useRecordedRequest reads one recorded call in the preview's shape; a record never changes. */
export function useRecordedRequest(
  sessionId: string,
  requestId: string,
): UseQueryResult<ModelContext> {
  return useQuery({
    queryKey: queryKeys.sessionRequest(sessionId, requestId),
    queryFn: ({ signal }) => getSessionRequest(sessionId, requestId, signal),
    enabled: requestId !== "",
    staleTime: Infinity,
  });
}
