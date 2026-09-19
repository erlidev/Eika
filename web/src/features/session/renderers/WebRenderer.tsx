/**
 * The `web_search` and `web_fetch` cards. A search shows its results as
 * links, read from the tool's details; a fetch shows what it asked for, how
 * the page met its budget, and the content the model received.
 */

import type { FetchDetails, SearchDetails, SearchResult } from "@/api/types";
import { safeHref } from "@/features/search";
import { FieldList, ResultBlock } from "@/features/session/renderers/parts";
import { numberArg, stringArg } from "@/features/session/renderers/registry";
import type { ToolRendererProps } from "@/features/session/renderers/registry";

/** searchDetails narrows a search call's details, undefined before it finishes. */
function searchDetails(details: unknown): SearchDetails | undefined {
  return typeof details === "object" && details !== null && "query" in details
    ? (details as SearchDetails)
    : undefined;
}

/** fetchDetails narrows a fetch call's details, undefined before it finishes. */
function fetchDetails(details: unknown): FetchDetails | undefined {
  return typeof details === "object" && details !== null && "url" in details
    ? (details as FetchDetails)
    : undefined;
}

/** WebSearchBody is the renderer body registered for `web_search`: the query and its results. */
export function WebSearchBody({ call }: ToolRendererProps) {
  const d = searchDetails(call.details);
  const results = d?.results ?? [];
  return (
    <div className="space-y-2">
      <FieldList
        fields={[
          ["query", stringArg(call, "query")],
          ["source", stringArg(call, "source") || "web"],
          ["count", String(numberArg(call, "count") ?? "")],
          ["answered by", (d?.providers ?? []).join(", ")],
          ["skipped", (d?.attempts ?? []).map((a) => `${a.provider}: ${a.error}`).join("; ")],
          ["cached", d?.cached ? "yes" : ""],
        ]}
      />
      {results.length > 0 && !call.isError ? (
        <ol className="space-y-2 text-sm">
          {results.map((r, i) => (
            <ResultLink key={`${String(i)}-${r.url}`} result={r} />
          ))}
        </ol>
      ) : (
        <ResultBlock call={call} label="search results" />
      )}
    </div>
  );
}

/** ResultLink is one search result: its title as a link, its URL, and its snippet. */
function ResultLink({ result }: { result: SearchResult }) {
  const href = safeHref(result.url);
  return (
    <li className="min-w-0">
      {href ? (
        <a
          href={href}
          target="_blank"
          rel="noreferrer noopener"
          className="font-medium hover:underline"
        >
          {result.title}
        </a>
      ) : (
        <span className="font-medium">{result.title}</span>
      )}
      <p className="text-muted-foreground truncate font-mono text-xs">{result.url}</p>
      {result.description && (
        <p className="text-muted-foreground line-clamp-2 text-xs">{result.description}</p>
      )}
    </li>
  );
}

/** WebFetchBody is the renderer body registered for `web_fetch`: the page, how it was narrowed, and its content. */
export function WebFetchBody({ call }: ToolRendererProps) {
  const d = fetchDetails(call.details);
  const final = d?.final_url && d.final_url !== d.url ? d.final_url : "";
  return (
    <div className="space-y-2">
      <FieldList
        fields={[
          ["url", stringArg(call, "url")],
          ["redirected to", final],
          ["section", stringArg(call, "section")],
          ["filter", stringArg(call, "filter")],
          ["format", stringArg(call, "format")],
          ["budget", d?.mode ?? (d?.filter_outcome ? `filter ${d.filter_outcome}` : "")],
          ["extracted from", d?.container ?? ""],
          ["cached", d?.cached ? "yes" : ""],
        ]}
      />
      <ResultBlock call={call} label="page content" />
    </div>
  );
}
