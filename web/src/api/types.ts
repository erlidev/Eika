/**
 * Wire types for the HTTP API. They mirror the Go structs in
 * `internal/server` exactly; the contract is documented in docs/api/http.md.
 * When a Go type changes, change these in the same commit.
 *
 * The types live in `api/types/`, a file per domain like the Go handlers;
 * import them from here, `@/api/types`.
 */

export * from "@/api/types/auth";
export * from "@/api/types/errors";
export * from "@/api/types/files";
export * from "@/api/types/mcp";
export * from "@/api/types/profiles";
export * from "@/api/types/projects";
export * from "@/api/types/providers";
export * from "@/api/types/requests";
export * from "@/api/types/runs";
export * from "@/api/types/search";
export * from "@/api/types/sessions";
export * from "@/api/types/settings";
export * from "@/api/types/system";
export * from "@/api/types/workspaces";
