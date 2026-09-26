/** Server state for the open session: its outline, its head, and its run. */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";

import { queryKeys } from "@/api/keys";
import {
  abortRun,
  answerElicitation,
  answerQuestion,
  forkSession,
  getRunStatus,
  getSession,
  getSessionOutline,
  listTools,
  postMessage,
  setSessionHead,
  setSessionTools,
} from "@/api/routes";
import type {
  ElicitationAnswer,
  Entry,
  PostMessage,
  Run,
  RunStatus,
  Session,
  SessionOutline,
  Tool,
} from "@/api/types";
import { useSessionStore } from "@/features/session/store";
import { rewindTarget } from "@/features/session/tree";

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

/** useAnswerElicitation delivers the user's answer to an MCP server that asked for input. */
export function useAnswerElicitation(
  sessionId: string,
): UseMutationResult<void, Error, { id: string; answer: ElicitationAnswer }> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, answer }) => answerElicitation(id, answer),
    // The run state is refetched, but until it arrives the answered request
    // must not show again from the copy held.
    onSuccess: (_, { id }) => {
      client.setQueryData<RunStatus>(queryKeys.runStatus(sessionId), (old) =>
        old === undefined
          ? old
          : { ...old, elicitations: old.elicitations.filter((x) => x.id !== id) },
      );
      return client.invalidateQueries({ queryKey: queryKeys.runStatus(sessionId) });
    },
  });
}

/**
 * useSetSessionHead moves the head so the next run continues from an entry.
 *
 * The transcript is rebuilt afterwards. A replay only ever adds entries, so
 * without discarding it the branch the head just left would stay on screen
 * and the next turn would read as continuing it.
 */
export function useSetSessionHead(sessionId: string): UseMutationResult<Session, Error, string> {
  const client = useQueryClient();
  const rewound = useSessionStore((s) => s.rewound);
  return useMutation({
    mutationFn: (entryId: string) => setSessionHead(sessionId, entryId),
    onSuccess: async () => {
      rewound();
      await client.invalidateQueries({ queryKey: queryKeys.session(sessionId) });
      await client.invalidateQueries({ queryKey: queryKeys.sessionOutline(sessionId) });
    },
  });
}

/**
 * useRewind takes the conversation back to just before a message the user
 * sent and puts that message back in the composer, which is what editing a
 * question and asking it again means. The turns after it stay in the tree on
 * a branch of their own; nothing is deleted.
 */
export function useRewind(
  sessionId: string,
): UseMutationResult<Session, Error, { entryId: string; text: string }> {
  const setHead = useSetSessionHead(sessionId);
  const outline = useSessionOutline(sessionId);
  const edit = useSessionStore((s) => s.edit);
  const nodes = outline.data?.nodes ?? [];
  return useMutation({
    mutationFn: ({ entryId }) => {
      const target = rewindTarget(nodes, entryId);
      if (target === null) {
        throw new Error("this message cannot be rewound to: the turn before it is unfinished");
      }
      return setHead.mutateAsync(target);
    },
    // The message goes back in the box only once the head has moved. Filling
    // it first would leave the text there after a refusal, next to the turns
    // it was meant to replace.
    onSuccess: (_session, { text }) => {
      edit(text);
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

/**
 * useTools lists every tool a run can offer. The built-in ones are the
 * harness's build and the MCP servers' change only with an `mcp.server`
 * event, which invalidates this, so it is never refetched otherwise.
 */
export function useTools(): UseQueryResult<Tool[]> {
  return useQuery({
    queryKey: queryKeys.tools(),
    queryFn: ({ signal }) => listTools(signal),
    staleTime: Infinity,
  });
}

/**
 * useSetSessionTools chooses the tools the session's next run offers the
 * model; null goes back to its profile's choice.
 */
export function useSetSessionTools(
  sessionId: string,
): UseMutationResult<Session, Error, string[] | null> {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (tools: string[] | null) => setSessionTools(sessionId, tools),
    onSuccess: async (session) => {
      client.setQueryData<{ session: Session; head?: Entry }>(
        queryKeys.session(sessionId),
        (old) => (old === undefined ? old : { ...old, session }),
      );
      await Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.sessionConfiguration(sessionId) }),
        client.invalidateQueries({ queryKey: queryKeys.sessionContext(sessionId) }),
        client.invalidateQueries({ queryKey: ["sessions"] }),
      ]);
    },
  });
}
