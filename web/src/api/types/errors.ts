/** Wire types for an API error body. */

/** ErrorCode is the machine-readable half of an API error body. */
export type ErrorCode =
  | "invalid_request"
  | "unauthorized"
  | "forbidden"
  | "not_found"
  | "conflict"
  | "too_large"
  | "internal";
