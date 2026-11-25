/** Server state for the request inspector. */
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import type { UseQueryResult } from "@tanstack/react-query";
import { queryKeys } from "@/api/keys";
import { getSessionCall, getSessionContext, listSessionCalls } from "@/api/routes";
import type { ModelCall, ModelContext } from "@/api/types";

export const nextRequest = "next";
export type RequestSelection = {
  selected: string;
  select: (id: string) => void;
  records: UseQueryResult<ModelCall[]>;
  shown: UseQueryResult<ModelContext>;
};
export function useRequestSelection(
  sessionId: string,
  model = "",
  previewEnabled = true,
): RequestSelection {
  const [selected, select] = useState(nextRequest);
  const records = useQuery({
    queryKey: [...queryKeys.sessionContext(sessionId), "calls"],
    queryFn: ({ signal }) => listSessionCalls(sessionId, signal),
  });
  const shown = useQuery({
    queryKey: [...queryKeys.sessionContext(sessionId), "inspection", selected, model],
    enabled: selected !== nextRequest || previewEnabled,
    queryFn: ({ signal }) =>
      selected === nextRequest
        ? getSessionContext(sessionId, model, signal)
        : getSessionCall(sessionId, selected, signal),
  });
  return { selected, select, records, shown };
}
