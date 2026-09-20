import { beforeEach, describe, expect, it } from "vitest";

import { connect } from "@/api/connection";
import type { WebSocketLike } from "@/api/stream";
import { decodeBase64, encodeBase64, TerminalSocket } from "@/api/terminal";
import type { TerminalStatus } from "@/api/terminal";

/** FakeSocket records what was sent and lets a test drive the callbacks. */
class FakeSocket implements WebSocketLike {
  readonly sent: string[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;

  send(data: string): void {
    this.sent.push(data);
  }
  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.onclose?.();
  }
  frames(): unknown[] {
    return this.sent.map((raw) => JSON.parse(raw) as unknown);
  }
  deliver(frame: unknown): void {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
}

function harness() {
  const urls: string[] = [];
  const socket = new FakeSocket();
  const output: Uint8Array[] = [];
  const statuses: [TerminalStatus, number | undefined][] = [];
  const terminal = new TerminalSocket(
    "ws 1",
    24,
    80,
    {
      output: (bytes) => output.push(bytes),
      status: (status, code) => statuses.push([status, code]),
    },
    {
      open: (url) => {
        urls.push(url);
        return socket;
      },
    },
  );
  return { terminal, socket, urls, output, statuses };
}

const text = new TextDecoder();

beforeEach(() => {
  connect({ baseUrl: "https://eika.example", token: "tok" });
});

describe("base64", () => {
  it("round-trips bytes that are not valid text on their own", () => {
    // The first two bytes of "é" and a lone continuation byte.
    const bytes = new Uint8Array([0xc3, 0xa9, 0x80, 0x00, 0xff]);
    expect(decodeBase64(encodeBase64(bytes))).toEqual(bytes);
  });

  it("decodes UTF-8 without passing it through a Latin-1 string", () => {
    const encoded = encodeBase64(new TextEncoder().encode("héllo ✓"));
    expect(text.decode(decodeBase64(encoded))).toBe("héllo ✓");
  });
});

describe("TerminalSocket", () => {
  it("opens the workspace's terminal with its size and the token", () => {
    const { urls } = harness();
    expect(urls).toEqual([
      "wss://eika.example/api/workspaces/ws%201/terminal?token=tok&rows=24&cols=80",
    ]);
  });

  it("holds input and the last resize until the socket opens", () => {
    const { terminal, socket, statuses } = harness();
    terminal.input("ls\r");
    terminal.resize(30, 100);
    terminal.resize(40, 120);
    expect(socket.sent).toEqual([]);
    socket.onopen?.();
    expect(socket.frames()).toEqual([
      { type: "resize", rows: 40, cols: 120 },
      { type: "input", data: encodeBase64(new TextEncoder().encode("ls\r")) },
    ]);
    expect(statuses).toEqual([["open", undefined]]);
  });

  it("sends typed text as UTF-8", () => {
    const { terminal, socket } = harness();
    socket.onopen?.();
    terminal.input("é");
    expect(socket.frames()).toEqual([{ type: "input", data: "w6k=" }]);
  });

  it("delivers output as bytes", () => {
    const { socket, output } = harness();
    socket.onopen?.();
    socket.deliver({ type: "output", data: encodeBase64(new TextEncoder().encode("$ ✓")) });
    expect(output.map((b) => text.decode(b))).toEqual(["$ ✓"]);
  });

  it("ignores frames it does not know", () => {
    const { socket, output, statuses } = harness();
    socket.onopen?.();
    socket.onmessage?.({ data: "not json" });
    socket.deliver({ type: "bell" });
    expect(output).toEqual([]);
    expect(statuses).toEqual([["open", undefined]]);
  });

  it("reports the exit code and closes, staying exited", () => {
    const { terminal, socket, statuses } = harness();
    socket.onopen?.();
    socket.deliver({ type: "exit", exit_code: 3 });
    expect(socket.closed).toBe(true);
    expect(statuses).toEqual([
      ["open", undefined],
      ["exited", 3],
    ]);
    terminal.input("more");
    expect(socket.sent).toEqual([]);
  });

  it("reads an exit without a code as a clean exit", () => {
    const { socket, statuses } = harness();
    socket.onopen?.();
    socket.deliver({ type: "exit" });
    expect(statuses.at(-1)).toEqual(["exited", 0]);
  });

  it("reports a socket that never opened as failed", () => {
    const { terminal, socket, statuses } = harness();
    socket.onerror?.();
    expect(terminal.getStatus()).toBe("failed");
    expect(statuses).toEqual([["failed", undefined]]);
  });

  it("reports a dropped connection as closed", () => {
    const { terminal, socket, statuses } = harness();
    socket.onopen?.();
    socket.onerror?.();
    expect(terminal.getStatus()).toBe("closed");
    expect(statuses.at(-1)).toEqual(["closed", undefined]);
  });

  it("drops a resize to nothing, which a hidden terminal reports", () => {
    const { terminal, socket } = harness();
    socket.onopen?.();
    terminal.resize(0, 0);
    expect(socket.sent).toEqual([]);
  });
});
