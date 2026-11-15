/** Server state for Chat: the home session it shows. */

import { useQuery } from "@tanstack/react-query";
import type { UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { getHome } from "@/api/routes";
import type { Entry, Session } from "@/api/types";

/**
 * useHome reads the home session, which the harness makes when it starts. It
 * never changes identity, so the stream does not refresh it; the session
 * view reads the session itself by its id and follows its events.
 */
export function useHome(): UseQueryResult<{ session: Session; head?: Entry }> {
  return useQuery({
    queryKey: queryKeys.home(),
    queryFn: () => getHome(),
  });
}
