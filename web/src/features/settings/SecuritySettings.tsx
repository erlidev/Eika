/**
 * Settings, Security: the text ingest removed from tool results and
 * subagent reports, newest first, each with where it came from. Reading a row
 * is the only thing the page does; trusting a source and the guard's health
 * arrive with later packages.
 */

import { useInfiniteQuery } from "@tanstack/react-query";

import { getWithheld } from "@/api/routes";
import { queryKeys } from "@/api/keys";
import type { Withheld } from "@/api/types";
import { LoadError } from "@/components/Notice";
import { ScrollRegion } from "@/components/ScrollRegion";
import { Button } from "@/components/ui/button";
import { formatAgo } from "@/lib/format";

/** sourceLabels name where removed text came from, as the user would say it. */
const sourceLabels: Record<string, string> = {
  web_fetch: "Fetched page",
  web_search: "Search result",
  report: "Subagent report",
  plugin: "Plugin",
};

export function SecuritySettings() {
  const withheld = useInfiniteQuery({
    queryKey: queryKeys.withheld(),
    queryFn: ({ pageParam, signal }) => getWithheld(pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next_before,
  });
  const rows = withheld.data?.pages.flatMap((page) => page.withheld) ?? [];
  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <h3 className="text-sm font-medium">Withheld</h3>
        <p className="text-muted-foreground text-xs">
          Text the screen removed before the model saw it, newest first. The tool card it came from
          is marked.
        </p>
      </div>
      {withheld.isError && (
        <LoadError
          what="the withheld list"
          error={withheld.error}
          retry={() => void withheld.refetch()}
          retrying={withheld.isFetching}
        />
      )}
      {withheld.isPending && <p className="text-muted-foreground text-xs">Loading…</p>}
      {withheld.isSuccess && rows.length === 0 && (
        <p className="text-muted-foreground rounded-md border p-3 text-xs">
          Nothing has been withheld.
        </p>
      )}
      {rows.length > 0 && (
        <ul className="space-y-2">
          {rows.map((row) => (
            <WithheldRow key={row.id} row={row} />
          ))}
        </ul>
      )}
      {withheld.hasNextPage && (
        <Button
          variant="secondary"
          size="sm"
          disabled={withheld.isFetchingNextPage}
          onClick={() => void withheld.fetchNextPage()}
        >
          {withheld.isFetchingNextPage ? "Loading…" : "Load older"}
        </Button>
      )}
    </div>
  );
}

type WithheldRowProps = { row: Withheld };

function WithheldRow({ row }: WithheldRowProps) {
  return (
    <li className="space-y-1.5 rounded-md border p-3">
      <div className="flex items-baseline justify-between gap-2 text-xs">
        <span className="font-medium">{sourceLabels[row.source] ?? row.source}</span>
        <time dateTime={row.at} className="text-muted-foreground shrink-0">
          {formatAgo(row.at)}
        </time>
      </div>
      <p className="text-muted-foreground font-mono text-xs break-all">{row.source_name}</p>
      <ScrollRegion
        label={`Removed text from ${row.source_name}`}
        className="bg-muted/50 max-h-40 rounded-md p-2"
      >
        <p className="font-mono text-xs break-words whitespace-pre-wrap">{row.text}</p>
      </ScrollRegion>
    </li>
  );
}
