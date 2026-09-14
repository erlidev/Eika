import { beforeEach, describe, expect, it } from "vitest";

import { connect } from "@/api/connection";
import { EventStream } from "@/api/stream";
import type { WebSocketLike } from "@/api/stream";

/** FakeSocket records what was sent and lets a test drive the callbacks. */
class FakeSocket implements WebSocketLike {
  readonly sent: string[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;

  send(data: string): void {
    this.sent.push(data);
  }
  close(): void {
    this.onclose?.();
  }
  requests(): unknown[] {
    return this.sent.map((raw) => JSON.parse(raw) as unknown);
  }
  deliver(event: unknown): void {
    this.onmessage?.({ data: JSON.stringify(event) });
  }
}

/**
 * harness builds a stream over fake sockets, a manual reconnect clock, and a
 * clock the test advances itself. Jitter is pinned to the middle of its range
 * so a delay is exactly the backoff the table names.
 */
function harness() {
  const sockets: FakeSocket[] = [];
  const urls: string[] = [];
  const timers: { fn: () => void; ms: number }[] = [];
  const clock = { ms: 0 };
  const stream = new EventStream({
    open: (url) => {
      urls.push(url);
      const socket = new FakeSocket();
      sockets.push(socket);
      return socket;
    },
    delay: (fn, ms) => timers.push({ fn, ms }),
    now: () => clock.ms,
    jitter: () => 0.5,
  });
  /** advance moves the clock, for a connection that stayed up a while. */
  const advance = (ms: number) => {
    clock.ms += ms;
  };
  return { stream, sockets, urls, timers, advance };
}

const message = {
  type: "message.delta",
  topic: "session:s1",
  time: "2026-01-01T00:00:00Z",
  payload: { run_id: "r1", text: "hi" },
};

beforeEach(() => {
  connect({ baseUrl: "http://harness.test", token: "secret" });
});

describe("EventStream", () => {
  it("connects with the token and the topics it was asked for", () => {
    const { stream, urls, sockets } = harness();
    stream.subscribe("session:s1", () => undefined);
    const url = new URL(urls[0] ?? "");
    expect(url.protocol).toBe("ws:");
    expect(url.pathname).toBe("/api/events");
    expect(url.searchParams.get("token")).toBe("secret");
    expect(url.searchParams.get("topics")).toBe("session:s1");

    sockets[0]?.onopen?.();
    expect(sockets[0]?.requests()[0]).toEqual({ type: "subscribe", topics: ["session:s1"] });
  });

  it("delivers an event only to the handlers of its topic", () => {
    const { stream, sockets } = harness();
    const mine: unknown[] = [];
    const other: unknown[] = [];
    stream.subscribe("session:s1", (e) => mine.push(e));
    stream.subscribe("session:s2", (e) => other.push(e));
    sockets[0]?.onopen?.();
    sockets[0]?.deliver(message);
    expect(mine).toHaveLength(1);
    expect(other).toHaveLength(0);
  });

  it("delivers bus.dropped to every handler, whatever its topic", () => {
    const { stream, sockets } = harness();
    const seen: string[] = [];
    stream.subscribe("session:s1", () => seen.push("s1"));
    stream.subscribe("workspace:w1", () => seen.push("w1"));
    sockets[0]?.onopen?.();
    sockets[0]?.deliver({
      type: "bus.dropped",
      topic: "global",
      time: "2026-01-01T00:00:00Z",
      payload: { dropped: 3 },
    });
    expect(seen.sort()).toEqual(["s1", "w1"]);
  });

  it("reference-counts topics and unsubscribes only the last holder", () => {
    const { stream, sockets } = harness();
    const first = stream.subscribe("session:s1", () => undefined);
    stream.subscribe("session:s1", () => undefined);
    sockets[0]?.onopen?.();
    const before = sockets[0]?.sent.length ?? 0;

    first();
    expect(sockets[0]?.requests().at(-1)).toEqual({ type: "subscribe", topics: ["session:s1"] });
    expect(sockets[0]?.sent.length).toBe(before + 1);
  });

  it("holds a replay asked for before the socket opens", () => {
    const { stream, sockets } = harness();
    stream.subscribe("session:s1", () => undefined);
    stream.replay("s1", "e4");
    expect(sockets[0]?.sent).toHaveLength(0);

    sockets[0]?.onopen?.();
    expect(sockets[0]?.requests()).toEqual([
      { type: "subscribe", topics: ["session:s1"] },
      { type: "session.replay", session_id: "s1", since: "e4" },
    ]);
  });

  it("reconnects with backoff and re-sends its topics", () => {
    const { stream, sockets, timers, advance } = harness();
    stream.subscribe("session:s1", () => undefined);
    sockets[0]?.onopen?.();
    advance(30000);
    sockets[0]?.close();
    expect(stream.getStatus()).toBe("reconnecting");
    expect(timers[0]?.ms).toBe(500);

    timers[0]?.fn();
    expect(sockets).toHaveLength(2);
    sockets[1]?.onopen?.();
    expect(stream.getStatus()).toBe("open");
    expect(sockets[1]?.requests()[0]).toEqual({ type: "subscribe", topics: ["session:s1"] });

    advance(30000);
    sockets[1]?.close();
    expect(timers[1]?.ms).toBe(500);
  });

  it("lengthens the backoff while connecting keeps failing", () => {
    const { stream, sockets, timers } = harness();
    stream.subscribe("session:s1", () => undefined);
    sockets[0]?.close();
    timers[0]?.fn();
    sockets[1]?.close();
    expect(timers.map((timer) => timer.ms)).toEqual([500, 1000]);
  });

  it("keeps lengthening the backoff when a socket opens and drops at once", () => {
    const { stream, sockets, timers, advance } = harness();
    stream.subscribe("session:s1", () => undefined);
    // A server that accepts the handshake and then hangs up — an auth
    // rejection after the upgrade — is the same outage, not a success.
    for (let i = 0; i < 3; i += 1) {
      sockets[i]?.onopen?.();
      advance(20);
      sockets[i]?.close();
      timers[i]?.fn();
    }
    expect(timers.map((timer) => timer.ms)).toEqual([500, 1000, 2000]);
  });

  it("spreads the reconnect delay around the backoff it is given", () => {
    const timers: { fn: () => void; ms: number }[] = [];
    const sockets: FakeSocket[] = [];
    const stream = new EventStream({
      open: () => {
        const socket = new FakeSocket();
        sockets.push(socket);
        return socket;
      },
      delay: (fn, ms) => timers.push({ fn, ms }),
      now: () => 0,
      jitter: () => 0,
    });
    stream.subscribe("session:s1", () => undefined);
    sockets[0]?.close();
    expect(timers[0]?.ms).toBe(400);
  });

  it("tells its listeners the connection came back, but not that it began", () => {
    const { stream, sockets, timers } = harness();
    let reopens = 0;
    const off = stream.onReopen(() => {
      reopens += 1;
    });
    stream.subscribe("session:s1", () => undefined);
    sockets[0]?.onopen?.();
    expect(reopens).toBe(0);

    sockets[0]?.close();
    timers[0]?.fn();
    sockets[1]?.onopen?.();
    expect(reopens).toBe(1);

    off();
    sockets[1]?.close();
    timers[1]?.fn();
    sockets[2]?.onopen?.();
    expect(reopens).toBe(1);
  });

  it("stops reconnecting once nothing is subscribed", () => {
    const { stream, sockets, timers } = harness();
    const off = stream.subscribe("session:s1", () => undefined);
    sockets[0]?.onopen?.();
    off();
    sockets[0]?.close();
    expect(timers).toHaveLength(0);
    expect(stream.getStatus()).toBe("idle");
  });

  it("ignores a frame that is not a known event", () => {
    const { stream, sockets } = harness();
    const seen: unknown[] = [];
    stream.subscribe("session:s1", (e) => seen.push(e));
    sockets[0]?.onopen?.();
    sockets[0]?.onmessage?.({ data: "not json" });
    sockets[0]?.deliver({ type: "nope", topic: "session:s1", time: "2026-01-01T00:00:00Z" });
    expect(seen).toHaveLength(0);
  });
});
