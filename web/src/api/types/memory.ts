/** Wire types for the user's memory API; see docs/api/http.md, "Memory". */

export type MemoryType = "user" | "preference" | "person" | "fact" | "procedure";
export type MemoryState = "active" | "pending" | "archived";

/** MemoryItem is one memory in a list or search result, without its body. */
export type MemoryItem = {
  name: string;
  description: string;
  type: MemoryType;
  state: MemoryState;
  pinned: boolean;
  /** expires, updated and content_sha are set by a list. */
  expires?: string;
  updated?: string;
  content_sha?: string;
  /** snippet is the start of the body, set by a search. */
  snippet?: string;
};

/** MemoryPage is a page of memories; next_cursor is absent on the last page. */
export type MemoryPage = {
  memories: MemoryItem[];
  next_cursor?: string;
};

/** MemoryScreenSpan is one screened window at or over the threshold. */
export type MemoryScreenSpan = {
  field: "description" | "body";
  start: number;
  end: number;
  score: number;
  text: string;
};

/** MemoryScreen is the verdict behind a pending memory. */
export type MemoryScreen = {
  spans: MemoryScreenSpan[];
  max_score: number;
  unscanned: boolean;
};

/** Memory is one memory in full. content_sha is the stale-edit precondition. */
export type Memory = {
  name: string;
  description: string;
  type: MemoryType;
  source: string;
  created: string;
  updated: string;
  /** expires is a date, or "" for none. */
  expires: string;
  pinned: boolean;
  state: MemoryState;
  body: string;
  content_sha: string;
  screen?: MemoryScreen;
  archive_reason?: string;
};

/** MemoryListQuery filters and pages GET /api/memories; q makes it a search. */
export type MemoryListQuery = {
  q?: string;
  type?: MemoryType;
  state?: MemoryState;
  pinned?: boolean;
  include_archived?: boolean;
  limit?: number;
  after?: string;
};

/** EditMemoryRequest changes the fields given; expires "" clears it. */
export type EditMemoryRequest = {
  content_sha: string;
  description?: string;
  type?: MemoryType;
  body?: string;
  expires?: string;
};
