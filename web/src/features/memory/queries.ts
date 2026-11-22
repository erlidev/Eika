/** Server state for the Memory page: the filtered list, paged, and one memory in full. */

import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import type { UseInfiniteQueryResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import { getMemory, listMemories, listSessions } from "@/api/routes";
import type { Memory, MemoryItem, Session } from "@/api/types";
import { listQuery, narrow } from "@/features/memory/state";
import type { Filters } from "@/features/memory/state";

const pageSize = 30;

/** useMemories pages through the memories the filters select; a search is one page. */
export function useMemories(
  filters: Filters,
): UseInfiniteQueryResult<{ items: MemoryItem[]; more: boolean }> {
  const query = listQuery(filters);
  return useInfiniteQuery({
    queryKey: [...queryKeys.memories(), "list", query] as const,
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) =>
      listMemories(
        { ...query, limit: pageSize, ...(pageParam ? { after: pageParam } : {}) },
        signal,
      ),
    getNextPageParam: (page) => page.next_cursor,
    select: (data) => ({
      items: narrow(
        data.pages.flatMap((p) => p.memories),
        filters,
      ),
      more: data.pages[data.pages.length - 1]?.next_cursor !== undefined,
    }),
  });
}

/** useMemory reads one memory in full. */
export function useMemory(name: string): UseQueryResult<Memory> {
  return useQuery({
    queryKey: queryKeys.memory(name),
    queryFn: ({ signal }) => getMemory(name, signal),
  });
}

/** useSources lists the sessions a memory's source can link to. */
export function useSources(): UseQueryResult<Session[]> {
  return useQuery({
    queryKey: queryKeys.allSessions(),
    queryFn: ({ signal }) => listSessions(signal),
  });
}
