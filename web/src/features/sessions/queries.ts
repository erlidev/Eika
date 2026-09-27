/** Server state for the session list of a workspace. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { globalTopic, payloadOf } from "@/api/events";
import { queryKeys } from "@/api/keys";
import { createSession, deleteSession, listChats, listSessions, updateSession } from "@/api/routes";
import type { CreateSession, Session, UpdateSession } from "@/api/types";
import { useStreamSubscription } from "@/api/useStream";

/**
 * useSessions lists the sessions of one workspace, or all of them.
 * `descendants` also brings back the forks and child agents they led to,
 * which the sidebar hangs under the session each came from.
 */
export function useSessions(
  workspaceId?: string,
  descendants?: boolean,
): UseQueryResult<Session[]> {
  return useQuery({
    queryKey: queryKeys.sessions(workspaceId, descendants),
    queryFn: ({ signal }) => listSessions(workspaceId, descendants, signal),
  });
}

/**
 * useChats lists the sessions with no workspace. The forks of a chat are
 * chats too, so the list holds them and the sidebar nests them.
 */
export function useChats(): UseQueryResult<Session[]> {
  return useQuery({
    queryKey: queryKeys.chats(),
    queryFn: ({ signal }) => listChats(signal),
  });
}

/** useCreateSession opens a session in a workspace, or a chat in none. */
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
    const payload = payloadOf(e, "session.title") ?? payloadOf(e, "session.updated");
    if (!payload) return;
    void client.invalidateQueries({ queryKey: ["sessions"] });
    void client.invalidateQueries({ queryKey: queryKeys.session(payload.session_id), exact: true });
  });
}
