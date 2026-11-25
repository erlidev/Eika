/** Withheld is one removal: text ingest kept out of a session's context. */
export type Withheld = {
  id: string;
  at: string;
  /** source is web_fetch, web_search, report or plugin. */
  source: string;
  /** source_name is the URL, query or tool it came from. */
  source_name: string;
  session_id: string;
  run_id: string;
  /** call_key is `<session id>:<tool call id>`, empty when there was no tool call. */
  call_key: string;
  text: string;
  score: number;
};

/** WithheldPage is a page of withheld rows, newest first. */
export type WithheldPage = {
  withheld: Withheld[];
  next_before?: string;
};
