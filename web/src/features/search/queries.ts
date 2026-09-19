/** Server state for search: the status report, the keys, and trying a search. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { getSearchStatus, putSearchKey, runSearch } from "@/api/routes";
import type { SearchKey, SearchOutcome, SearchRequest, SearchStatus } from "@/api/types";

/** useSearchStatus reports every backend's health, the stored keys, and the caches. */
export function useSearchStatus(): UseQueryResult<SearchStatus> {
  return useQuery({
    queryKey: queryKeys.searchStatus(),
    queryFn: ({ signal }) => getSearchStatus(signal),
  });
}

/** useSaveSearchKey stores a key, or removes it when the key is empty. */
export function useSaveSearchKey(): UseMutationResult<
  SearchKey[],
  Error,
  { name: string; key: string }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ name, key }) => putSearchKey(name, key),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: queryKeys.searchStatus() });
    },
  });
}

/** useTrySearch runs one search, as web_search would, and refreshes the status it changed. */
export function useTrySearch(): UseMutationResult<SearchOutcome, Error, SearchRequest> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: runSearch,
    onSettled: async () => {
      await client.invalidateQueries({ queryKey: queryKeys.searchStatus() });
    },
  });
}
