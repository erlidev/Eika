/** Server state for the list of chats. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { globalTopic, payloadOf } from "@/api/events";
import { queryKeys } from "@/api/keys";
import {
  createSession,
  deleteSession,
  listChats,
  listComputers,
  listComputerSessions,
  updateSession,
} from "@/api/routes";
import type { Computer, CreateSession, Session, UpdateSession } from "@/api/types";
import { useStreamSubscription } from "@/api/useStream";

/**
 * useChats lists the chats: the sessions with no computer.
 */
export function useChats(): UseQueryResult<Session[]> {
  return useQuery({
    queryKey: queryKeys.chats(),
    queryFn: ({ signal }) => listChats(signal),
  });
}

/**
 * useHomeComputer reads the home computer, which the harness makes on its
 * own: undefined until it has recorded it. A `computer.state` event on the
 * global topic says it has (see useSessionUpdates), so nothing polls.
 */
export function useHomeComputer(): UseQueryResult<Computer | undefined> {
  return useQuery({
    queryKey: queryKeys.computers(),
    queryFn: async ({ signal }) => (await listComputers(signal)).find((cx) => cx.home),
  });
}

/** useHomeSessions lists the sessions that run on the home computer; none without it. */
export function useHomeSessions(homeId: string | undefined): UseQueryResult<Session[]> {
  return useQuery({
    queryKey: queryKeys.computerSessions(homeId ?? ""),
    queryFn: ({ signal }) => listComputerSessions(homeId ?? "", signal),
    enabled: homeId !== undefined,
  });
}

/** useCreateSession opens a chat, or a session on a computer. */
export function useCreateSession(): UseMutationResult<Session, Error, CreateSession> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: createSession,
    onSuccess: () => client.invalidateQueries({ queryKey: ["sessions"] }),
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

/** useDeleteSession aborts a session's run and deletes it. */
export function useDeleteSession(): UseMutationResult<void, Error, string> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: deleteSession,
    onSuccess: () => client.invalidateQueries({ queryKey: ["sessions"] }),
  });
}

/**
 * useSessionUpdates keeps every session list current as sessions change
 * without this client asking: a `session.title` event on the global topic
 * says an untitled session was named, a `session.updated` one that someone
 * renamed, pinned, or archived one. The workbench calls it once, so a change
 * arrives in the sidebar whichever session is open.
 */
export function useSessionUpdates(): void {
  const client = useQueryClient();
  useStreamSubscription(globalTopic, (e) => {
    // The harness announces the home computer on the global topic when it
    // records it, which is how the sidebar learns it can offer it.
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
