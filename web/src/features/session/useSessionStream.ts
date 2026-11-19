/**
 * Connects the open session to the event stream: it folds every event of the
 * session topic into the store, loads stored pages, and invalidates the server
 * state a turn changes.
 */

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";

import { sessionTopic } from "@/api/events";
import { queryKeys } from "@/api/keys";
import { eventStream } from "@/api/stream";
import { getRunStatus, getSessionEntries, getSessionOutline } from "@/api/routes";
import { readCatchUp } from "@/features/session/pages";
import { useStreamStatus, useStreamSubscription } from "@/api/useStream";
import { useSessionStore } from "@/features/session/store";

/**
 * useSessionStream subscribes the store to one session and loads its pages.
 */
export type TranscriptPages = {
  hasOlder: boolean;
  loading: boolean;
  error: unknown;
  loadOlder: (beforeUpdate: () => void) => void;
  retry: () => void;
  pageVersion: number;
};

export function useSessionStream(sessionId: string | undefined): TranscriptPages {
  const client = useQueryClient();
  const open = useSessionStore((s) => s.open);
  const apply = useSessionStore((s) => s.apply);
  const replayRequested = useSessionStore((s) => s.replayRequested);
  const needsReplay = useSessionStore((s) => s.needsReplay);
  const status = useStreamStatus();
  const [ready, setReady] = useState(false);
  const [catching, setCatching] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<unknown>();
  const [cursor, setCursor] = useState<string>();
  const [pageVersion, setPageVersion] = useState(0);
  const generation = useRef(0);
  const olderInFlight = useRef(false);
  const retryOlder = useRef<(() => void) | undefined>(undefined);
  const openSessionId = useSessionStore((s) => s.sessionId);

  useStreamSubscription(sessionId === undefined ? undefined : sessionTopic(sessionId), (e) => {
    apply(e);
    // A turn the client did not start, such as a background turn that
    // delivers notices, is first known from its turn.start: the run status
    // says a run is going, so the composer steers it instead of starting one.
    if (e.type === "session.updated") {
      useSessionStore.getState().replayNeeded();
      void client.invalidateQueries({
        queryKey: queryKeys.runStatus(e.topic.slice("session:".length)),
      });
    }
    if (e.type === "turn.start") {
      void client.invalidateQueries({
        queryKey: queryKeys.runStatus(e.topic.slice("session:".length)),
      });
    }
    if (
      e.type === "turn.end" ||
      e.type === "run.error" ||
      e.type === "question.asked" ||
      e.type === "mcp.elicitation" ||
      e.type === "compaction.start" ||
      e.type === "compaction.end"
    ) {
      void client.invalidateQueries({
        queryKey: queryKeys.runStatus(e.topic.slice("session:".length)),
      });
      void client.invalidateQueries({
        queryKey: queryKeys.sessionOutline(e.topic.slice("session:".length)),
      });
      // A turn that ended, and a compaction, changed the next request.
      if (e.type === "turn.end" || e.type === "run.error" || e.type === "compaction.end") {
        void client.invalidateQueries({
          queryKey: queryKeys.sessionContext(e.topic.slice("session:".length)),
        });
      }
    }
  });

  // Subscribe first. A catch-up after the socket opens covers the interval
  // between subscription and the first page response.
  useEffect(() => {
    if (sessionId === undefined) return;
    const current = ++generation.current;
    open(sessionId);
    // Reset the page state when the subscription changes sessions.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setReady(false);
    setCatching(false);
    setLoading(false);
    setCursor(undefined);
    setError(undefined);
    olderInFlight.current = false;
    retryOlder.current = undefined;
    const controller = new AbortController();
    let started = false;
    const start = () => {
      if (started) return;
      started = true;
      const revision = useSessionStore.getState().revision;
      void getSessionEntries(sessionId, undefined, controller.signal)
        .then(async (page) => {
          const outline = await getSessionOutline(sessionId, controller.signal);
          if (generation.current !== current) return;
          if (useSessionStore.getState().revision !== revision) {
            setReady(true);
            useSessionStore.getState().replayNeeded();
            return;
          }
          useSessionStore.getState().setOutline(sessionId, outline);
          useSessionStore.getState().addEntries(sessionId, page.entries);
          setCursor(page.next_before);
          setReady(true);
          useSessionStore.getState().replayNeeded();
        })
        .catch((err: unknown) => {
          if (generation.current === current && !controller.signal.aborted) setError(err);
        });
    };
    const unsubscribe = eventStream().onStatus((next) => {
      if (next === "open") start();
    });
    if (eventStream().getStatus() === "open") start();
    return () => {
      generation.current = current + 1;
      controller.abort();
      unsubscribe();
    };
  }, [sessionId, open]);

  useEffect(() => {
    if (sessionId === undefined || status !== "open") return;
    useSessionStore.getState().replayNeeded();
  }, [sessionId, status]);

  useEffect(() => {
    if (sessionId === undefined) return;
    return eventStream().onReopen(() => {
      useSessionStore.getState().replayNeeded();
    });
  }, [sessionId]);

  useEffect(() => {
    if (
      !ready ||
      !needsReplay ||
      catching ||
      sessionId === undefined ||
      openSessionId !== sessionId
    )
      return;
    const current = generation.current;
    const state = useSessionStore.getState();
    const newest = state.entries.at(-1)?.id;
    const revision = state.revision;
    const sealed = [...state.sealedTurns];
    const liveTurns = [
      ...new Set([...state.live.map((item) => item.runId), state.activeTurnId].filter(Boolean)),
    ];
    replayRequested();
    // The request runs until its promise settles.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setCatching(true);
    void (async () => {
      // Read status first: an idle snapshot predates the page read. New
      // live turns that arrive during either request remain in the store.
      const run = await getRunStatus(sessionId);
      const completed = run.active ? sealed : [...new Set([...sealed, ...liveTurns])];
      const page = await readCatchUp(
        (before) => getSessionEntries(sessionId, before),
        newest,
        newest === undefined && revision > 0,
      );
      const outline = await getSessionOutline(sessionId);
      if (generation.current === current && useSessionStore.getState().revision === revision) {
        useSessionStore.getState().setOutline(sessionId, outline);
        if (newest === undefined) setCursor(revision > 0 ? page.nextBefore : undefined);
        useSessionStore.getState().addEntries(sessionId, page.entries, completed);
        setError(undefined);
      }
    })()
      .catch((err: unknown) => {
        if (generation.current === current) setError(err);
      })
      .finally(() => {
        if (generation.current === current) setCatching(false);
      });
  }, [ready, needsReplay, catching, sessionId, openSessionId, replayRequested]);

  const loadOlder = useCallback(
    (beforeUpdate: () => void) => {
      if (sessionId === undefined || cursor === undefined || olderInFlight.current) return;
      olderInFlight.current = true;
      const current = generation.current;
      const revision = useSessionStore.getState().revision;
      setLoading(true);
      void getSessionEntries(sessionId, cursor)
        .then((page) => {
          if (generation.current !== current || useSessionStore.getState().revision !== revision)
            return;
          beforeUpdate();
          useSessionStore.getState().addEntries(sessionId, page.entries);
          setCursor(page.next_before);
          setPageVersion((n) => n + 1);
          retryOlder.current = undefined;
          setError(undefined);
        })
        .catch((err: unknown) => {
          if (generation.current === current) {
            retryOlder.current = beforeUpdate;
            setError(err);
          }
        })
        .finally(() => {
          if (generation.current === current) {
            olderInFlight.current = false;
            setLoading(false);
          }
        });
    },
    [sessionId, cursor],
  );

  const retry = useCallback(() => {
    if (retryOlder.current !== undefined) {
      setError(undefined);
      loadOlder(retryOlder.current);
      return;
    }
    if (!ready && sessionId !== undefined) {
      setError(undefined);
      const current = generation.current;
      const revision = useSessionStore.getState().revision;
      void getSessionEntries(sessionId)
        .then(async (page) => {
          const outline = await getSessionOutline(sessionId);
          if (
            generation.current !== current ||
            useSessionStore.getState().sessionId !== sessionId ||
            useSessionStore.getState().revision !== revision
          )
            return;
          useSessionStore.getState().setOutline(sessionId, outline);
          useSessionStore.getState().addEntries(sessionId, page.entries);
          setCursor(page.next_before);
          setReady(true);
          useSessionStore.getState().replayNeeded();
        })
        .catch((err: unknown) => {
          if (generation.current === current) setError(err);
        });
    } else {
      setError(undefined);
      useSessionStore.getState().replayNeeded();
    }
  }, [ready, sessionId, loadOlder]);

  return { hasOlder: cursor !== undefined, loading, error, loadOlder, retry, pageVersion };
}
