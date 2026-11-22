/** The halt state: read once, then kept current by `halt.state` events. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useEffect } from "react";

import { globalTopic, payloadOf } from "@/api/events";
import { queryKeys } from "@/api/keys";
import { getHalt, halt, resumeHalt } from "@/api/routes";
import { eventStream } from "@/api/stream";
import type { Halt } from "@/api/types";
import { useStreamSubscription } from "@/api/useStream";

/** useHalt reads whether Eika is halted. */
export function useHalt(): UseQueryResult<Halt> {
  return useQuery({ queryKey: queryKeys.halt(), queryFn: ({ signal }) => getHalt(signal) });
}

/**
 * useHaltEvents applies each `halt.state` event to the cache and reads the
 * state again when the stream was missing events or came back, so a halt
 * that happened meanwhile still shows. The shell calls it once.
 */
export function useHaltEvents(): void {
  const client = useQueryClient();
  const refresh = () => client.invalidateQueries({ queryKey: queryKeys.halt() });
  useStreamSubscription(globalTopic, (e) => {
    if (e.type === "bus.dropped") {
      void refresh();
      return;
    }
    const state = payloadOf(e, "halt.state");
    if (state) client.setQueryData<Halt>(queryKeys.halt(), state);
  });
  useEffect(() => eventStream().onReopen(() => void refresh()));
}

/** useHaltNow stops everything. */
export function useHaltNow(): UseMutationResult<Halt, Error, void> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => halt(),
    onSuccess: (state) => {
      client.setQueryData<Halt>(queryKeys.halt(), state);
    },
  });
}

/** useResume lifts the halt. */
export function useResume(): UseMutationResult<Halt, Error, void> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: resumeHalt,
    onSuccess: (state) => {
      client.setQueryData<Halt>(queryKeys.halt(), state);
    },
  });
}
