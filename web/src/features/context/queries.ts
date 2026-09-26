/** Server state for the Context panel: the next request, and the recorded ones. */

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useState } from "react";
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

/** next is the request selection for the request a run started now would send. */
export const nextRequest = "next";

/** RequestSelection is which request the Context panel and inspector show, and what it is. */
export type RequestSelection = {
  /** selected is nextRequest or a recorded request's id. */
  selected: string;
  select: (id: string) => void;
  /** records are the session's recorded calls, oldest first. */
  records: UseQueryResult<ModelRequest[]>;
  /** shown is the selected request, assembled. */
  shown: UseQueryResult<ModelContext>;
};

/**
 * useRequestSelection holds which request is shown: the next one, previewed
 * live for the model the session has chosen, or a recorded one.
 */
export function useRequestSelection(sessionId: string, model: string): RequestSelection {
  const [selected, select] = useState(nextRequest);
  const records = useRecordedRequests(sessionId);
  const preview = useNextRequest(sessionId, model);
  const recorded = useRecordedRequest(sessionId, selected === nextRequest ? "" : selected);
  return { selected, select, records, shown: selected === nextRequest ? preview : recorded };
}
