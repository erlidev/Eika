/**
 * The one HTTP client the UI uses. Every route in docs/api/http.md goes
 * through `request`, which attaches the bearer token, decodes the JSON error
 * shape into an ApiError, and disconnects on 401.
 */

import { disconnect, getConnection, resolveUrl } from "@/api/connection";
import type { Connection } from "@/api/connection";
import type { ErrorCode } from "@/api/types";

/** ApiError is a failure the harness reported in its JSON error shape. */
export class ApiError extends Error {
  /** status is the HTTP status code. */
  readonly status: number;
  /** code is the harness's machine-readable reason. */
  readonly code: ErrorCode;

  constructor(status: number, code: ErrorCode, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

const codes: readonly ErrorCode[] = [
  "invalid_request",
  "unauthorized",
  "not_found",
  "conflict",
  "internal",
];

/** codeForStatus is the fallback when a failure carries no usable body. */
function codeForStatus(status: number): ErrorCode {
  switch (status) {
    case 400:
      return "invalid_request";
    case 401:
      return "unauthorized";
    case 404:
      return "not_found";
    case 409:
      return "conflict";
    default:
      return "internal";
  }
}

/**
 * parseApiError narrows a failure body to an ApiError. A proxy or a crash can
 * answer with something that is not the documented shape, so the status
 * decides the code whenever the body does not.
 */
export function parseApiError(status: number, body: unknown): ApiError {
  const fallback = new ApiError(status, codeForStatus(status), `request failed: ${String(status)}`);
  if (typeof body !== "object" || body === null) return fallback;
  const detail: unknown = (body as Record<string, unknown>).error;
  if (typeof detail !== "object" || detail === null) return fallback;
  const record = detail as Record<string, unknown>;
  const code = typeof record.code === "string" ? record.code : "";
  const message = typeof record.message === "string" ? record.message : "";
  if (message === "") return fallback;
  const known = codes.includes(code as ErrorCode) ? (code as ErrorCode) : codeForStatus(status);
  return new ApiError(status, known, message);
}

/** RequestOptions are the parts of a request that vary by route. */
export type RequestOptions = {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  /** body is encoded as JSON when present. */
  body?: unknown;
  /** query holds search parameters; entries with no value are left out. */
  query?: Record<string, string | undefined>;
  signal?: AbortSignal;
  /**
   * connection overrides the stored deployment. Only the connect screen uses
   * it, to try a token before storing it; a 401 on such a request leaves the
   * stored connection alone, because there is nothing to forget yet.
   */
  connection?: Connection;
};

function withQuery(path: string, query: RequestOptions["query"]): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, value);
  }
  const encoded = params.toString();
  return encoded === "" ? path : `${path}?${encoded}`;
}

/** request calls one API route and decodes its JSON body into `T`. */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const connection = options.connection ?? getConnection();
  const { token } = connection;
  const headers: Record<string, string> = { Accept: "application/json" };
  if (token !== "") headers.Authorization = `Bearer ${token}`;
  if (options.body !== undefined) headers["Content-Type"] = "application/json";

  const init: RequestInit = {
    method: options.method ?? "GET",
    headers,
  };
  if (options.body !== undefined) init.body = JSON.stringify(options.body);
  if (options.signal) init.signal = options.signal;

  const response = await fetch(
    resolveUrl(connection.baseUrl, withQuery(path, options.query)),
    init,
  );
  if (response.status === 204 || response.headers.get("Content-Length") === "0") {
    if (response.ok) return undefined as T;
  }

  let body: unknown = null;
  const text = await response.text();
  if (text !== "") {
    try {
      body = JSON.parse(text);
    } catch {
      body = null;
    }
  }

  if (!response.ok) {
    const error = parseApiError(response.status, body);
    // A stored token the harness refuses is worth nothing, so drop it and let
    // the connect screen ask for another.
    if (error.status === 401 && options.connection === undefined) disconnect();
    throw error;
  }
  return body as T;
}

/**
 * requestEmpty calls a route that answers 204 with no body. It exists so that
 * a route with nothing to decode does not have to name a response type.
 */
export async function requestEmpty(path: string, options: RequestOptions = {}): Promise<void> {
  await request<unknown>(path, options);
}
