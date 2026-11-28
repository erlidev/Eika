/** Server state for the Inbox: the pending count, the pending memories, and the answers. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useEffect } from "react";

import { inboxTopic, payloadOf } from "@/api/events";
import { queryKeys } from "@/api/keys";
import {
  acceptMemory,
  answerTripwire,
  getInboxCount,
  listInboxMemories,
  listInboxTripwires,
  rejectMemory,
} from "@/api/routes";
import { eventStream } from "@/api/stream";
import type {
  EditMemoryRequest,
  InboxCount,
  InboxPage,
  Memory,
  TripwireEntries,
  TripwireEntry,
} from "@/api/types";
import type { TripwireAnswer } from "@/features/inbox/state";
import { useStreamSubscription } from "@/api/useStream";

/** listLimit is the most the API returns at once; a longer Inbox shows its newest. */
export const listLimit = 50;

/** useInboxCount reads how many memories wait, for the badge. */
export function useInboxCount(): UseQueryResult<InboxCount> {
  return useQuery({
    queryKey: queryKeys.inboxCount(),
    queryFn: ({ signal }) => getInboxCount(signal),
  });
}

/** useInboxMemories lists the pending memories in full, newest first. */
export function useInboxMemories(): UseQueryResult<InboxPage> {
  return useQuery({
    queryKey: queryKeys.inboxList(),
    queryFn: ({ signal }) => listInboxMemories({ limit: listLimit }, signal),
  });
}

/** useInboxTripwires lists the open tripwire entries, newest first. */
export function useInboxTripwires(): UseQueryResult<TripwireEntries> {
  return useQuery({
    queryKey: queryKeys.inboxTripwires(),
    queryFn: ({ signal }) => listInboxTripwires(signal),
  });
}

/**
 * useInboxEvents keeps the badge and the lists current: `inbox.count` is the
 * badge, `memory.changed` and `tripwire.changed` make a list stale, and what a dropped or
 * reconnected stream missed is read again, since events are not replayed.
 * The shell calls it once, so the badge is live on every page.
 */
export function useInboxEvents(): void {
  const client = useQueryClient();
  const refresh = () => client.invalidateQueries({ queryKey: queryKeys.inbox() });
  useStreamSubscription(inboxTopic, (e) => {
    if (e.type === "bus.dropped") {
      void refresh();
      return;
    }
    const count = payloadOf(e, "inbox.count");
    if (count) client.setQueryData<InboxCount>(queryKeys.inboxCount(), { pending: count.pending });
    if (payloadOf(e, "memory.changed")) {
      void client.invalidateQueries({ queryKey: queryKeys.inboxList() });
    }
    if (payloadOf(e, "tripwire.changed")) {
      void client.invalidateQueries({ queryKey: queryKeys.inboxTripwires() });
    }
  });
  useEffect(() => eventStream().onReopen(() => void refresh()));
}

/** Answered is an answer to a pending memory: the memory and what to change on accept. */
export type AcceptInput = { name: string; edit: EditMemoryRequest };
export type RejectInput = { name: string; contentSha: string };

/** refreshAfter reads again what an answer, or its refusal, may have changed. */
function useRefresh(): () => Promise<void> {
  const client = useQueryClient();
  return async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: queryKeys.inbox() }),
      client.invalidateQueries({ queryKey: queryKeys.memories() }),
    ]);
  };
}

/** useAccept makes a pending memory active, with any edit; a refusal refreshes the card. */
export function useAccept(): UseMutationResult<Memory, Error, AcceptInput> {
  const refresh = useRefresh();
  return useMutation({
    mutationFn: ({ name, edit }) => acceptMemory(name, edit),
    onSettled: refresh,
  });
}

/** useReject archives a pending memory. */
export function useReject(): UseMutationResult<Memory, Error, RejectInput> {
  const refresh = useRefresh();
  return useMutation({
    mutationFn: ({ name, contentSha }) => rejectMemory(name, { content_sha: contentSha }),
    onSettled: refresh,
  });
}

/** useAnswerTripwire answers an entry; a refusal refreshes the card too. */
export function useAnswerTripwire(): UseMutationResult<
  TripwireEntry,
  Error,
  { id: string; answer: TripwireAnswer }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, answer }) => answerTripwire(id, answer),
    onSettled: () => client.invalidateQueries({ queryKey: queryKeys.inbox() }),
  });
}
