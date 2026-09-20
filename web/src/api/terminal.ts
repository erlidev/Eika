/**
 * The client for `GET /api/workspaces/{id}/terminal`, a WebSocket that
 * carries a shell's bytes as base64 in JSON text frames. It knows the frame
 * shapes and nothing about how the bytes are drawn; the terminal panel feeds
 * them to xterm. The protocol is documented in docs/api/http.md.
 */

import { terminalUrl } from "@/api/connection";
import type { WebSocketLike } from "@/api/stream";

/**
 * TerminalStatus is what the shell connection is doing. `failed` is a socket
 * that never opened: the harness refuses a terminal it cannot start (a
 * stopped workspace, a missing one) before the upgrade, so the browser learns
 * only that the connection failed.
 */
export type TerminalStatus = "connecting" | "open" | "exited" | "closed" | "failed";

/** TerminalHandlers receive what the shell sends. */
export type TerminalHandlers = {
  /** output delivers bytes the shell wrote, to be drawn as they are. */
  output: (bytes: Uint8Array) => void;
  /** status reports each change of the connection's state. */
  status: (status: TerminalStatus, exitCode?: number) => void;
};

/** TerminalOptions replace the browser socket so a test can drive the client. */
export type TerminalOptions = {
  open?: (url: string) => WebSocketLike;
};

/** TerminalFrame is one frame the harness sends. */
type TerminalFrame = { type: "output"; data: string } | { type: "exit"; exit_code: number };

const encoder = new TextEncoder();

/** encodeBase64 encodes bytes as standard base64. */
export function encodeBase64(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

/** decodeBase64 decodes standard base64 into bytes, not text: a frame may split a character. */
export function decodeBase64(text: string): Uint8Array {
  const binary = atob(text);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/** binaryStringBytes turns a string of byte values, as xterm reports mouse input, into bytes. */
export function binaryStringBytes(text: string): Uint8Array {
  const bytes = new Uint8Array(text.length);
  for (let i = 0; i < text.length; i++) bytes[i] = text.charCodeAt(i) & 0xff;
  return bytes;
}

function parseFrame(data: unknown): TerminalFrame | undefined {
  if (typeof data !== "string") return undefined;
  let parsed: unknown;
  try {
    parsed = JSON.parse(data);
  } catch {
    return undefined;
  }
  if (typeof parsed !== "object" || parsed === null) return undefined;
  const frame = parsed as Record<string, unknown>;
  if (frame.type === "output" && typeof frame.data === "string") {
    return { type: "output", data: frame.data };
  }
  if (frame.type === "exit") {
    // A clean exit may leave the code out.
    return { type: "exit", exit_code: typeof frame.exit_code === "number" ? frame.exit_code : 0 };
  }
  return undefined;
}

/**
 * TerminalSocket is one shell session. It opens on construction and ends when
 * the shell exits or close is called; a new session is a new TerminalSocket.
 */
export class TerminalSocket {
  private readonly socket: WebSocketLike;
  private readonly handlers: TerminalHandlers;
  private status: TerminalStatus = "connecting";
  /** pending holds input typed before the socket opened. */
  private pending: string[] = [];
  /** size is the latest size asked for before the socket opened. */
  private size: { rows: number; cols: number } | undefined;

  constructor(
    workspaceId: string,
    rows: number,
    cols: number,
    handlers: TerminalHandlers,
    options: TerminalOptions = {},
  ) {
    this.handlers = handlers;
    const open = options.open ?? ((url) => new WebSocket(url) as unknown as WebSocketLike);
    this.socket = open(terminalUrl(workspaceId, rows, cols));
    this.socket.onopen = () => {
      this.setStatus("open");
      if (this.size) this.send({ type: "resize", ...this.size });
      for (const data of this.pending) this.send({ type: "input", data });
      this.pending = [];
      this.size = undefined;
    };
    this.socket.onmessage = (message) => {
      const frame = parseFrame(message.data);
      if (!frame) return;
      if (frame.type === "output") {
        this.handlers.output(decodeBase64(frame.data));
        return;
      }
      this.setStatus("exited", frame.exit_code);
      this.socket.close();
    };
    this.socket.onerror = () => {
      this.socket.close();
    };
    this.socket.onclose = () => {
      if (this.status === "connecting") this.setStatus("failed");
      else if (this.status === "open") this.setStatus("closed");
    };
  }

  /** getStatus reports what the connection is doing. */
  getStatus(): TerminalStatus {
    return this.status;
  }

  /** input sends what the user typed, as UTF-8. */
  input(text: string): void {
    this.inputBytes(encoder.encode(text));
  }

  /** inputBytes sends raw bytes, such as a mouse report. */
  inputBytes(bytes: Uint8Array): void {
    const data = encodeBase64(bytes);
    if (this.status === "connecting") this.pending.push(data);
    else if (this.status === "open") this.send({ type: "input", data });
  }

  /** resize tells the shell the terminal's new size. */
  resize(rows: number, cols: number): void {
    if (rows <= 0 || cols <= 0) return;
    if (this.status === "connecting") this.size = { rows, cols };
    else if (this.status === "open") this.send({ type: "resize", rows, cols });
  }

  /** close ends the session. */
  close(): void {
    if (this.status === "connecting" || this.status === "open") this.setStatus("closed");
    this.socket.close();
  }

  private send(frame: object): void {
    this.socket.send(JSON.stringify(frame));
  }

  private setStatus(next: TerminalStatus, exitCode?: number): void {
    if (this.status === next) return;
    this.status = next;
    this.handlers.status(next, exitCode);
  }
}
