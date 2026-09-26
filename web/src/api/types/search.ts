/** Wire types for search: its status, keys, quotas, and results. */

/** SearchLimit is a quota on one search bucket. An absent or zero field is unlimited. */
export type SearchLimit = {
  day?: number;
  month?: number;
};

/** SearchUsage is one bucket's counters, in UTC days and months. */
export type SearchUsage = {
  day: string;
  day_used: number;
  month: string;
  month_used: number;
  cooldown_until?: string;
  fail_streak?: number;
};

/** SearchBackendStatus is the health of one search backend. */
export type SearchBackendStatus = {
  name: string;
  /** web marks a provider in the web failover chain; the others are sources. */
  web: boolean;
  key?: string;
  key_required?: boolean;
  key_set: boolean;
  bucket?: string;
  /** state is "ready", "no API key", or why the bucket is blocked. */
  state: string;
  usage: SearchUsage;
  limit: SearchLimit;
  /** probe says whether a self-hosted backend answers. */
  probe?: string;
};

/** SearchKey is one search API key. The key itself never leaves the harness. */
export type SearchKey = {
  name: string;
  set: boolean;
  hint?: string;
  updated_at?: string;
};

/** SearchStatus is the body of GET /api/search/status. */
export type SearchStatus = {
  order: string[];
  backends: SearchBackendStatus[];
  cached_searches: number;
  keys: SearchKey[];
  cached_pages: number;
  searxng_url: string;
};

/** SearchRequest is the body of POST /api/search, and web_search's arguments. */
export type SearchRequest = {
  query: string;
  source?: string;
  count?: number;
};

/** SearchResult is one search hit. */
export type SearchResult = {
  title: string;
  url: string;
  description?: string;
};

/** SearchDetails describe how a search went: web_search's tool details. */
export type SearchDetails = {
  source: string;
  query: string;
  count: number;
  providers?: string[];
  attempts?: { provider: string; error: string }[];
  cached?: boolean;
  pool?: number;
  results?: SearchResult[];
  ms: number;
};

/** SearchOutcome is the body of POST /api/search. */
export type SearchOutcome = {
  text: string;
  is_error: boolean;
  details: SearchDetails;
};

/** FetchDetails describe how a fetch went: web_fetch's tool details. */
export type FetchDetails = {
  url: string;
  format: string;
  section?: string;
  filter?: string;
  section_matched?: boolean;
  final_url?: string;
  container?: string;
  content_type?: string;
  bytes?: number;
  body_truncated?: boolean;
  cached?: boolean;
  mode?: "full" | "truncated" | "outline";
  budget_truncated?: boolean;
  headings?: number;
  filter_outcome?: "ok" | "empty" | "error";
  ms: number;
};
