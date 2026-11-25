/** Keeps the Skills page current: a skill that changed, on any client, is read again. */

import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import { payloadOf, skillsTopic } from "@/api/events";
import { queryKeys } from "@/api/keys";
import { eventStream } from "@/api/stream";
import { useStreamSubscription } from "@/api/useStream";

/**
 * useSkillsEvents invalidates every skills query on `skills.changed`, and when
 * the stream dropped events or came back, since events are not replayed. The
 * shell calls it once.
 */
export function useSkillsEvents(): void {
  const client = useQueryClient();
  const refresh = () => client.invalidateQueries({ queryKey: queryKeys.skills() });
  useStreamSubscription(skillsTopic, (e) => {
    if (e.type === "bus.dropped" || payloadOf(e, "skills.changed")) void refresh();
  });
  useEffect(() => eventStream().onReopen(() => void refresh()));
}
