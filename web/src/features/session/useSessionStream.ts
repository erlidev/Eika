/**
 * Connects the open session to the event stream: it folds every event of the
 * session topic into the store, asks for the replay that fills the transcript,
 * and invalidates the server state a turn changes.
 */

import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import { sessionTopic } from "@/api/events";
import { queryKeys } from "@/api/keys";
import { eventStream } from "@/api/stream";
import { useStreamSubscription } from "@/api/useStream";
import { useSessionStore } from "@/features/session/store";

/**
 * useSessionStream subscribes the store to one session. It returns nothing:
 * the transcript is read from the store, which every panel already shares.
 */
export function useSessionStream(sessionId: string | undefined): void {
  const client = useQueryClient();
  const open = useSessionStore((s) => s.open);
  const apply = useSessionStore((s) => s.apply);
  const replayRequested = useSessionStore((s) => s.replayRequested);
  const needsReplay = useSessionStore((s) => s.needsReplay);
  const lastEntryId = useSessionStore((s) => s.lastEntryId);
  const openSessionId = useSessionStore((s) => s.sessionId);

  useStreamSubscription(sessionId === undefined ? undefined : sessionTopic(sessionId), (e) => {
    apply(e);
    if (e.type === "turn.end" || e.type === "run.error" || e.type === "question.asked") {
      void client.invalidateQueries({
        queryKey: queryKeys.runStatus(e.topic.slice("session:".length)),
      });
      void client.invalidateQueries({
        queryKey: queryKeys.sessionOutline(e.topic.slice("session:".length)),
      });
    }
  });

  // Opening a session asks for its whole path; every later replay resumes
  // after the newest entry already held.
  useEffect(() => {
    if (sessionId === undefined) return;
    open(sessionId);
    eventStream().replay(sessionId);
    replayRequested();
  }, [sessionId, open, replayRequested]);

  // A turn that ended, or events the connection dropped, leave the transcript
  // behind what the harness stored. One replay catches it up.
  useEffect(() => {
    if (!needsReplay || sessionId === undefined || openSessionId !== sessionId) return;
    eventStream().replay(sessionId, lastEntryId);
    replayRequested();
  }, [needsReplay, sessionId, openSessionId, lastEntryId, replayRequested]);
}
