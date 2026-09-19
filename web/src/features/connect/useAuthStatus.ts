/** Whether the harness this browser points at has been set up. */

import { useQuery } from "@tanstack/react-query";
import type { UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { getAuthStatus } from "@/api/routes";
import type { AuthStatus } from "@/api/types";
import { useConnection } from "@/features/connect/useConnection";

/**
 * useAuthStatus asks the harness whether a sign-in password exists. It needs
 * no token, so it is what the app reads before it knows whether to offer
 * setup or sign-in.
 */
export function useAuthStatus(): UseQueryResult<AuthStatus> {
  const connection = useConnection();
  return useQuery({
    queryKey: [...queryKeys.authStatus(), connection.baseUrl],
    queryFn: ({ signal }) => getAuthStatus(connection, signal),
  });
}
