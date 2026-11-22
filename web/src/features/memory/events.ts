/** Keeps the Memory page current: a memory that changed, on any client, is read again. */

import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import { inboxTopic, payloadOf } from "@/api/events";
import { queryKeys } from "@/api/keys";
import { eventStream } from "@/api/stream";
import { useStreamSubscription } from "@/api/useStream";

/**
 * useMemoryEvents invalidates every memory query on `memory.changed`, and when
 * the stream dropped events or came back, since events are not replayed. The
 * shell calls it once.
 */
export function useMemoryEvents(): void {
  const client = useQueryClient();
  const refresh = () => client.invalidateQueries({ queryKey: queryKeys.memories() });
  useStreamSubscription(inboxTopic, (e) => {
    if (e.type === "bus.dropped" || payloadOf(e, "memory.changed")) void refresh();
  });
  useEffect(() => eventStream().onReopen(() => void refresh()));
}
