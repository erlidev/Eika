/** Server state about sessions as a set: the home computer, renaming, pinning, and archiving one, and keeping every cached session current. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { QueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { globalTopic, payloadOf } from "@/api/events";
import { queryKeys } from "@/api/keys";
import { eventStream } from "@/api/stream";
import { listComputers, updateSession } from "@/api/routes";
import type { Computer, Session, UpdateSession } from "@/api/types";
import { useStreamSubscription } from "@/api/useStream";

/**
 * useHomeComputer reads the home computer, which the harness makes on its
 * own: undefined until it has recorded it. A `computer.state` event on the
 * global topic says it has (see useSessionUpdates), so nothing polls.
 *
 * A query function may not return undefined, so the cache holds null for "no
 * home computer yet" and `select` hands the component undefined.
 */
export function useHomeComputer(): UseQueryResult<Computer | undefined> {
  return useQuery({
    queryKey: queryKeys.computers(),
    queryFn: async ({ signal }) => (await listComputers(signal)).find((cx) => cx.home) ?? null,
    select: (home) => home ?? undefined,
  });
}

/**
 * useUpdateSession renames, pins, or archives a session. The session.updated
 * event refreshes the lists too, but the change must not wait for the stream.
 */
export function useUpdateSession(): UseMutationResult<
  Session,
  Error,
  { id: string; changes: UpdateSession }
> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, changes }) => updateSession(id, changes),
    onSuccess: async (session) => {
      await client.invalidateQueries({ queryKey: ["sessions"] });
      await client.invalidateQueries({ queryKey: queryKeys.session(session.id), exact: true });
    },
  });
}

/**
 * refreshAll reads every list of sessions and every open session again: what
 * a lost event announced is not known, so nothing cached about sessions is
 * trusted.
 */
function refreshAll(client: QueryClient): Promise<void> {
  return Promise.all([
    client.invalidateQueries({ queryKey: ["sessions"] }),
    client.invalidateQueries({ queryKey: ["session"] }),
  ]).then(() => undefined);
}

/**
 * useSessionUpdates keeps every session list current as sessions change
 * without this client asking: a `session.title` event on the global topic
 * says an untitled session was named, a `session.updated` one that someone
 * renamed, pinned, or archived one. The shell calls it once, so a change
 * arrives whichever session is open. Events published while
 * the socket was down, or that the bus dropped for this client, are gone, so
 * the lists are read again when it comes back or the bus says it dropped some.
 */
export function useSessionUpdates(): void {
  const client = useQueryClient();
  useEffect(
    () =>
      eventStream().onReopen(() => {
        void refreshAll(client);
      }),
    [client],
  );
  useStreamSubscription(globalTopic, (e) => {
    if (payloadOf(e, "bus.dropped")) {
      void refreshAll(client);
      return;
    }
    // The harness announces the home computer on the global topic when it
    // records it, which is how the session view learns of it.
    if (payloadOf(e, "computer.state")) {
      void client.invalidateQueries({ queryKey: queryKeys.computers() });
      return;
    }
    const payload = payloadOf(e, "session.title") ?? payloadOf(e, "session.updated");
    if (!payload) return;
    void client.invalidateQueries({ queryKey: ["sessions"] });
    void client.invalidateQueries({ queryKey: queryKeys.session(payload.session_id), exact: true });
  });
}
