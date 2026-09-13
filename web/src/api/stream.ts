/**
 * The WebSocket client for `GET /api/events`. One connection serves the whole
 * page: subscribers reference-count topics, the socket reconnects with
 * backoff, and a reconnect re-sends the union of the live topics so nothing
 * has to be re-subscribed by hand.
 *
 * The protocol is documented in docs/api/events.md.
 */

import { eventStreamUrl } from "@/api/connection";
import { globalTopic, parseEvent } from "@/api/events";
import type { EikaEvent, StreamRequest } from "@/api/events";

/** StreamStatus is what the single connection is doing. */
export type StreamStatus = "idle" | "connecting" | "open" | "reconnecting";

/** EventHandler receives every event on a topic it subscribed to. */
export type EventHandler = (e: EikaEvent) => void;

/** StreamOptions replace the browser pieces so a test can drive the stream. */
export type StreamOptions = {
  /** open builds a socket for a URL. Defaults to the global WebSocket. */
  open?: (url: string) => WebSocketLike;
  /** delay schedules a reconnect. Defaults to setTimeout. */
  delay?: (fn: () => void, ms: number) => void;
};

/** WebSocketLike is the part of WebSocket the stream uses. */
export type WebSocketLike = {
  send(data: string): void;
  close(): void;
  onopen: (() => void) | null;
  onclose: (() => void) | null;
  onerror: (() => void) | null;
  onmessage: ((e: { data: unknown }) => void) | null;
};

/** backoffMs is the reconnect delay per consecutive failure, then repeats. */
const backoffMs = [500, 1000, 2000, 5000, 10000] as const;

/**
 * EventStream multiplexes the harness event stream. Create one per page and
 * share it; `subscribe` is the only way in and returns its own unsubscribe.
 */
export class EventStream {
  private socket: WebSocketLike | null = null;
  private status: StreamStatus = "idle";
  private failures = 0;
  private closed = false;
  private readonly topics = new Map<string, number>();
  private readonly handlers = new Map<string, Set<EventHandler>>();
  private queued: StreamRequest[] = [];
  private readonly statusListeners = new Set<(s: StreamStatus) => void>();
  private readonly open: (url: string) => WebSocketLike;
  private readonly delay: (fn: () => void, ms: number) => void;

  constructor(options: StreamOptions = {}) {
    this.open = options.open ?? ((url) => new WebSocket(url) as unknown as WebSocketLike);
    this.delay =
      options.delay ??
      ((fn, ms) => {
        setTimeout(fn, ms);
      });
  }

  /** getStatus reports what the connection is doing. */
  getStatus(): StreamStatus {
    return this.status;
  }

  /** onStatus registers a status listener and returns its unsubscribe. */
  onStatus(listener: (s: StreamStatus) => void): () => void {
    this.statusListeners.add(listener);
    return () => {
      this.statusListeners.delete(listener);
    };
  }

  /**
   * subscribe adds a handler for one topic, connecting or widening the
   * subscription as needed. The returned function removes the handler and
   * drops the topic when it was the last one holding it.
   */
  subscribe(topic: string, handler: EventHandler): () => void {
    let set = this.handlers.get(topic);
    if (!set) {
      set = new Set();
      this.handlers.set(topic, set);
    }
    set.add(handler);
    this.topics.set(topic, (this.topics.get(topic) ?? 0) + 1);
    this.ensureOpen();
    this.sendTopics();

    return () => {
      const handlersForTopic = this.handlers.get(topic);
      handlersForTopic?.delete(handler);
      const count = (this.topics.get(topic) ?? 1) - 1;
      if (count <= 0) {
        this.topics.delete(topic);
        this.handlers.delete(topic);
      } else {
        this.topics.set(topic, count);
      }
      this.sendTopics();
    };
  }

  /**
   * replay asks the harness to re-send a session's path as session.message
   * events on this connection. `since` continues after an entry the client
   * already holds; without it the whole path arrives.
   */
  replay(sessionId: string, since?: string): void {
    const request: StreamRequest =
      since === undefined || since === ""
        ? { type: "session.replay", session_id: sessionId }
        : { type: "session.replay", session_id: sessionId, since };
    // A replay asked for before the socket is open is the normal case: the
    // session view mounts and connects in the same render. Hold it until the
    // connection can carry it.
    if (this.status === "open" && this.socket) {
      this.socket.send(JSON.stringify(request));
      return;
    }
    this.queued.push(request);
    this.ensureOpen();
  }

  /** close shuts the connection down for good. */
  close(): void {
    this.closed = true;
    this.socket?.close();
    this.socket = null;
    this.setStatus("idle");
  }

  private setStatus(next: StreamStatus): void {
    if (this.status === next) return;
    this.status = next;
    for (const listener of this.statusListeners) listener(next);
  }

  private send(request: StreamRequest): void {
    if (this.status !== "open" || !this.socket) return;
    this.socket.send(JSON.stringify(request));
  }

  private sendTopics(): void {
    this.send({ type: "subscribe", topics: [...this.topics.keys()] });
  }

  private ensureOpen(): void {
    if (this.closed || this.socket) return;
    this.setStatus(this.failures === 0 ? "connecting" : "reconnecting");
    // The first connection carries its topics in the URL, so a client that
    // never gets to send a frame still receives what it asked for.
    const socket = this.open(eventStreamUrl([...this.topics.keys()]));
    this.socket = socket;

    socket.onopen = () => {
      this.failures = 0;
      this.setStatus("open");
      this.sendTopics();
      const queued = this.queued;
      this.queued = [];
      for (const request of queued) socket.send(JSON.stringify(request));
    };
    socket.onerror = () => {
      socket.close();
    };
    socket.onclose = () => {
      if (this.socket !== socket) return;
      this.socket = null;
      if (this.closed || this.topics.size === 0) {
        this.setStatus("idle");
        return;
      }
      this.setStatus("reconnecting");
      const wait = backoffMs[Math.min(this.failures, backoffMs.length - 1)] ?? 10000;
      this.failures += 1;
      this.delay(() => {
        this.ensureOpen();
      }, wait);
    };
    socket.onmessage = (message) => {
      this.dispatch(message.data);
    };
  }

  private dispatch(data: unknown): void {
    if (typeof data !== "string") return;
    let parsed: unknown;
    try {
      parsed = JSON.parse(data);
    } catch {
      return;
    }
    let e: EikaEvent;
    try {
      e = parseEvent(parsed);
    } catch {
      // An event type this build does not know is not worth tearing the
      // connection down for; a later phase adds the type.
      return;
    }
    // bus.dropped reports on the connection, not on a topic, so everyone
    // hears it and decides whether to replay.
    if (e.type === "bus.dropped") {
      for (const set of this.handlers.values()) {
        for (const handler of set) handler(e);
      }
      return;
    }
    const set = this.handlers.get(e.topic);
    if (!set) return;
    for (const handler of set) handler(e);
  }
}

/** The stream every hook shares. */
let shared: EventStream | null = null;

/** eventStream returns the page's one stream, creating it on first use. */
export function eventStream(): EventStream {
  shared ??= new EventStream();
  return shared;
}

/** resetEventStream drops the shared stream, for a disconnect or a test. */
export function resetEventStream(): void {
  shared?.close();
  shared = null;
}

export { globalTopic };
