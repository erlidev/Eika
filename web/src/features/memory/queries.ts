/** Server state for the Memory page: the filtered list, paged, and one memory in full. */

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  QueryClient,
  UseInfiniteQueryResult,
  UseMutationResult,
  UseQueryResult,
} from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import {
  archiveMemory,
  editMemory,
  getMemory,
  listMemories,
  listSessions,
  purgeMemory,
  setMemoryPinned,
} from "@/api/routes";
import type { EditMemoryRequest, Memory, MemoryItem, Session } from "@/api/types";
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

/** changed puts a changed memory in its detail and refreshes every list and search. */
function changed(client: QueryClient, memory: Memory) {
  client.setQueryData(queryKeys.memory(memory.name), memory);
  void client.invalidateQueries({ queryKey: [...queryKeys.memories(), "list"] });
}

/** useEditMemory saves the fields given; the answer says whether the edit went pending. */
export function useEditMemory(name: string): UseMutationResult<Memory, Error, EditMemoryRequest> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: EditMemoryRequest) => editMemory(name, input),
    onSuccess: (memory) => {
      changed(client, memory);
    },
  });
}

/** useSetPinned pins or unpins a memory read at `contentSha`. */
export function useSetPinned(
  name: string,
): UseMutationResult<Memory, Error, { pinned: boolean; contentSha: string }> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ pinned, contentSha }: { pinned: boolean; contentSha: string }) =>
      setMemoryPinned(name, pinned, contentSha),
    onSuccess: (memory) => {
      changed(client, memory);
    },
  });
}

/** useArchiveMemory archives a memory read at `contentSha`. */
export function useArchiveMemory(name: string): UseMutationResult<Memory, Error, string> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (contentSha: string) => archiveMemory(name, undefined, contentSha),
    onSuccess: (memory) => {
      changed(client, memory);
    },
  });
}

/** usePurgeMemory removes a memory read at `contentSha`. */
export function usePurgeMemory(name: string): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (contentSha: string) => purgeMemory(name, contentSha),
    onSuccess: () => {
      client.removeQueries({ queryKey: queryKeys.memory(name) });
      void client.invalidateQueries({ queryKey: [...queryKeys.memories(), "list"] });
    },
  });
}
