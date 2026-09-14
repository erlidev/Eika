/** Server state for the open session: its outline, its head, and its run. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import {
  abortRun,
  answerQuestion,
  forkSession,
  getRunStatus,
  getSession,
  getSessionOutline,
  postMessage,
  setSessionHead,
} from "@/api/routes";
import type { Entry, PostMessage, Run, Session, SessionOutline } from "@/api/types";

/** useSession reads a session with the entry its head points at. */
export function useSession(
  id: string | undefined,
): UseQueryResult<{ session: Session; head?: Entry }> {
  return useQuery({
    queryKey: queryKeys.session(id ?? ""),
    queryFn: ({ signal }) => getSession(id ?? "", signal),
    enabled: id !== undefined,
  });
}

/** useSessionOutline reads the whole tree the session tree panel draws. */
export function useSessionOutline(id: string | undefined): UseQueryResult<SessionOutline> {
  return useQuery({
    queryKey: queryKeys.sessionOutline(id ?? ""),
    queryFn: ({ signal }) => getSessionOutline(id ?? "", signal),
    enabled: id !== undefined,
  });
}

/**
 * useRunStatus reads what the session is doing. The stream keeps it current,
 * so it is not polled.
 */
export function useRunStatus(id: string | undefined) {
  return useQuery({
    queryKey: queryKeys.runStatus(id ?? ""),
    queryFn: ({ signal }) => getRunStatus(id ?? "", signal),
    enabled: id !== undefined,
  });
}

/** usePostMessage runs, steers, or queues a follow-up on a session. */
export function usePostMessage(sessionId: string): UseMutationResult<Run, Error, PostMessage> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: PostMessage) => postMessage(sessionId, input),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.runStatus(sessionId) }),
  });
}

/** useAbortRun cancels the run in progress. */
export function useAbortRun(sessionId: string): UseMutationResult<Run, Error, string> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: abortRun,
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.runStatus(sessionId) }),
  });
}

/** useAnswerQuestion delivers the answer an `ask_user` call waits for. */
export function useAnswerQuestion(
  sessionId: string,
): UseMutationResult<void, Error, { id: string; answer: string }> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, answer }) => answerQuestion(id, answer),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.runStatus(sessionId) }),
  });
}

/** useSetSessionHead moves the head so the next run continues from an entry. */
export function useSetSessionHead(sessionId: string): UseMutationResult<Session, Error, string> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (entryId: string) => setSessionHead(sessionId, entryId),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: queryKeys.session(sessionId) });
      await client.invalidateQueries({ queryKey: queryKeys.sessionOutline(sessionId) });
    },
  });
}

/** useForkSession copies the path down to an entry into a session of its own. */
export function useForkSession(
  sessionId: string,
): UseMutationResult<Session, Error, { entryId: string; title: string }> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ entryId, title }) => forkSession(sessionId, entryId, title),
    onSuccess: () => client.invalidateQueries({ queryKey: ["sessions"] }),
  });
}
