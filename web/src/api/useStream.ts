/**
 * React access to the one event stream. A component names a topic and gets
 * its events; the hook owns the subscription and releases it on unmount, so
 * the reference counting in `stream.ts` is the only bookkeeping there is.
 */

import { useEffect, useRef, useSyncExternalStore } from "react";

import type { EikaEvent } from "@/api/events";
import { eventStream } from "@/api/stream";
import type { StreamStatus } from "@/api/stream";

/**
 * useStreamSubscription delivers one topic's events to a handler. The handler
 * may change every render; the subscription does not.
 */
export function useStreamSubscription(
  topic: string | undefined,
  handler: (e: EikaEvent) => void,
): void {
  const latest = useRef(handler);
  // The subscription must not be torn down when the handler identity changes,
  // so the current handler is kept in a ref that an effect updates.
  useEffect(() => {
    latest.current = handler;
  });

  useEffect(() => {
    if (topic === undefined || topic === "") return;
    return eventStream().subscribe(topic, (e) => {
      latest.current(e);
    });
  }, [topic]);
}

/** useStreamStatus reports what the connection is doing, for the status bar. */
export function useStreamStatus(): StreamStatus {
  return useSyncExternalStore(
    (listener) => eventStream().onStatus(listener),
    () => eventStream().getStatus(),
    () => "idle" as const,
  );
}
