/**
 * What every route file of the mock harness shares: the shape of a reply and
 * a handler, the context a route file registers its routes with, and the
 * helpers that build replies and narrow request bodies.
 */

import type { EikaEvent, EventType } from "../../../src/api/events.ts";
import type {
  ElicitationAnswer,
  MessageImage,
  Run,
  Session,
  Computer,
} from "../../../src/api/types.ts";
import type { World } from "../world.ts";

/** Reply is how the mock answers a request: a status and a JSON body, if any. */
export type Reply = { status: number; body?: unknown };

export type Handler = (req: {
  params: string[];
  query: URLSearchParams;
  body: Record<string, unknown>;
  /** raw is the body as sent, for a route whose body is not JSON. */
  raw: string;
  /**
   * invalidUtf8 is set when the bytes sent were not UTF-8, which `raw` and
   * `body` then hold with replacement characters. The routes whose harness
   * handler reads the bytes first refuse the request before they look at it.
   */
  invalidUtf8?: boolean;
}) => Reply | Promise<Reply>;

/**
 * RouteContext is what MockHarness hands each route file: the world, `on` to
 * register a route, the lookups, and the parts of the harness a handler
 * drives (ids, events, runs, and the answers a run waits for).
 */
export type RouteContext = {
  w: World;
  on: (method: string, path: string, handle: Handler, auth?: boolean) => void;
  find: <T extends { id: string }>(rows: T[], id: string | undefined, what: string) => T | Reply;
  now: () => string;
  /** running finds a computer that must be running, or the refusal if it is not. */
  running: (id: string | undefined) => Computer | Reply;
  nextId: (prefix: string) => string;
  emit: (event: EikaEvent) => void;
  event: (type: EventType, topic: string, payload?: unknown) => EikaEvent;
  nameSession: (session: Session, text: string) => void;
  finish: (run: Run, state: Run["state"], error?: string) => void;
  play: (session: Session, run: Run, text: string, images: MessageImage[]) => Promise<void>;
  /** compact summarizes a session's conversation as a run of its own. */
  compact: (session: Session, run: Run, instructions: string) => Promise<void>;
  answers: Map<string, (answer: string) => void>;
  elicitAnswers: Map<string, (answer: ElicitationAnswer) => void>;
  /** authorizations maps an OAuth state the mock handed out to the server it authorizes. */
  authorizations: Map<string, string>;
};

export function ok(body: unknown, status = 200): Reply {
  return { status, body };
}

/**
 * badUtf8 is the harness's refusal of a body whose bytes are not UTF-8, which
 * encoding/json would otherwise replace with U+FFFD and store. It answers for
 * the routes that check the bytes before decoding, or undefined to go on.
 */
export function badUtf8(req: { invalidUtf8?: boolean }): Reply | undefined {
  return req.invalidUtf8 === true
    ? fail(400, "invalid_request", "request body must be valid UTF-8")
    : undefined;
}

export function fail(status: number, code: string, message: string): Reply {
  return { status, body: { error: { code, message } } };
}

/**
 * checkName trims a title or a name the user chose, or answers the refusal
 * the harness gives an empty one or one past 200 characters.
 */
export function checkName(field: string, value: unknown): string | Reply {
  const name = str(value).trim();
  if (name === "") return fail(400, "invalid_request", `${field} must not be empty`);
  if (Array.from(name).length > 200) {
    return fail(400, "invalid_request", `${field} is longer than 200 characters`);
  }
  return name;
}

export function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/** strings narrows a JSON body field to the list of strings it should be. */
export function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
}
