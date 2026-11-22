import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  addBlockedHost,
  getBlockedHosts,
  removeBlockedHost,
  addTrustedSource,
  getTrustedSources,
  getWithheld,
  removeTrustedSource,
  trustWithheld,
} from "@/api/routes";
import { queryKeys } from "@/api/keys";
import type { Withheld } from "@/api/types";
import { ActionError, LoadError } from "@/components/Notice";
import { ScrollRegion } from "@/components/ScrollRegion";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { useState } from "react";
import { useGuard } from "@/features/settings/guard";
import { useSaveSettings, useSettings } from "@/features/settings/queries";
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
    <div className="space-y-4">
      <ScreenSettings />
      <BlockedHosts />
      <TrustedSources />
      <div className="space-y-1">
        <h3 className="text-sm font-medium">Withheld</h3>
        <p className="text-muted-foreground text-xs">
          Suspect text recorded by the screen, newest first. Text from a trusted host stays in the
          result. The tool card is marked when text was removed.
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
      {(row.source === "web_fetch" || row.source === "web_search") && webHost(row.source_name) && (
        <TrustButton id={row.id} />
      )}
      <ScrollRegion
        label={`Removed text from ${row.source_name}`}
        className="bg-muted/50 max-h-40 rounded-md p-2"
      >
        <p className="font-mono text-xs break-words whitespace-pre-wrap">{row.text}</p>
      </ScrollRegion>
    </li>
  );
}

function webHost(value: string): boolean {
  try {
    const url = new URL(value);
    return (url.protocol === "http:" || url.protocol === "https:") && !!url.hostname;
  } catch {
    return false;
  }
}

function useTrustMutation(fn: (value: string) => Promise<void>) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.trustedSources() }),
  });
}

function TrustButton({ id }: { id: string }) {
  const trust = useTrustMutation(trustWithheld);
  return (
    <div>
      <Button
        size="sm"
        variant="outline"
        disabled={trust.isPending}
        onClick={() => {
          trust.mutate(id);
        }}
      >
        Trust this source
      </Button>
      {trust.isError && <ActionError action="trust the source" error={trust.error} />}
    </div>
  );
}

function useBlockMutation(fn: (value: string) => Promise<void>) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.blockedHosts() }),
  });
}

function BlockedHosts() {
  const sources = useQuery({
    queryKey: queryKeys.blockedHosts(),
    queryFn: ({ signal }) => getBlockedHosts(signal),
  });
  const add = useBlockMutation(addBlockedHost);
  const remove = useBlockMutation(removeBlockedHost);
  const [host, setHost] = useState("");
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">Blocked hosts</h3>
      <p className="text-muted-foreground text-xs">
        Computers, web fetch and search cannot connect to blocked hosts. Blocking applies to the
        exact host, not its subdomains.
      </p>
      {sources.isError && (
        <LoadError
          what="blocked hosts"
          error={sources.error}
          retry={() => void sources.refetch()}
          retrying={sources.isFetching}
        />
      )}
      {sources.isPending && <p className="text-muted-foreground text-xs">Loading…</p>}
      {sources.isSuccess && sources.data.hosts.length === 0 && (
        <p className="text-muted-foreground text-xs">No blocked hosts.</p>
      )}
      {sources.data && sources.data.hosts.length > 0 && (
        <ul className="space-y-2">
          {sources.data.hosts.map((item) => (
            <li key={item} className="flex items-center gap-2 rounded-md border p-2">
              <span className="min-w-0 flex-1 break-all text-xs">{item}</span>
              <Button
                size="sm"
                variant="outline"
                aria-label={`Remove ${item}`}
                disabled={remove.isPending}
                onClick={() => {
                  remove.mutate(item);
                }}
              >
                Remove
              </Button>
            </li>
          ))}
        </ul>
      )}
      <form
        className="space-y-2"
        onSubmit={(event) => {
          event.preventDefault();
          add.mutate(host.trim(), {
            onSuccess: () => {
              setHost("");
            },
          });
        }}
      >
        <Label htmlFor="blocked-host">Host to block</Label>
        <div className="flex gap-2">
          <Input
            id="blocked-host"
            value={host}
            onChange={(event) => {
              setHost(event.target.value);
            }}
            placeholder="example.com"
          />
          <Button type="submit" disabled={!host.trim() || add.isPending}>
            Add
          </Button>
        </div>
        <p className="text-muted-foreground text-xs">
          Enter a host without a scheme, port or path.
        </p>
      </form>
      {add.isError && <ActionError action="block the host" error={add.error} />}
      {remove.isError && <ActionError action="remove the blocked host" error={remove.error} />}
    </section>
  );
}

function TrustedSources() {
  const sources = useQuery({
    queryKey: queryKeys.trustedSources(),
    queryFn: ({ signal }) => getTrustedSources(signal),
  });
  const add = useTrustMutation(addTrustedSource);
  const remove = useTrustMutation(removeTrustedSource);
  const [host, setHost] = useState("");
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">Trusted sources</h3>
      <p className="text-muted-foreground text-xs">
        Trusted hosts are still screened, but suspect text stays in the result. Trust applies to the
        exact host, not its subdomains.
      </p>
      {sources.isError && (
        <LoadError
          what="trusted sources"
          error={sources.error}
          retry={() => void sources.refetch()}
          retrying={sources.isFetching}
        />
      )}
      {sources.isPending && <p className="text-muted-foreground text-xs">Loading…</p>}
      {sources.isSuccess && sources.data.hosts.length === 0 && (
        <p className="text-muted-foreground text-xs">No trusted sources.</p>
      )}
      {sources.data && sources.data.hosts.length > 0 && (
        <ul className="space-y-2">
          {sources.data.hosts.map((item) => (
            <li key={item} className="flex items-center gap-2 rounded-md border p-2">
              <span className="min-w-0 flex-1 break-all text-xs">{item}</span>
              <Button
                size="sm"
                variant="outline"
                aria-label={`Remove ${item}`}
                disabled={remove.isPending}
                onClick={() => {
                  remove.mutate(item);
                }}
              >
                Remove
              </Button>
            </li>
          ))}
        </ul>
      )}
      <form
        className="space-y-2"
        onSubmit={(event) => {
          event.preventDefault();
          add.mutate(host.trim(), {
            onSuccess: () => {
              setHost("");
            },
          });
        }}
      >
        <Label htmlFor="trusted-host">Host to trust</Label>
        <div className="flex gap-2">
          <Input
            id="trusted-host"
            value={host}
            onChange={(event) => {
              setHost(event.target.value);
            }}
            placeholder="example.com"
          />
          <Button type="submit" disabled={!host.trim() || add.isPending}>
            Add
          </Button>
        </div>
        <p className="text-muted-foreground text-xs">
          Enter a host without a scheme, port or path.
        </p>
      </form>
      {add.isError && <ActionError action="add the source" error={add.error} />}
      {remove.isError && <ActionError action="remove the source" error={remove.error} />}
    </section>
  );
}

function ScreenSettings() {
  const guard = useGuard();
  const settings = useSettings();
  const save = useSaveSettings();
  const [threshold, setThreshold] = useState<string | null>(null);
  const state = settings.data;
  const current =
    typeof state?.settings.screen_threshold === "number"
      ? state.settings.screen_threshold
      : state?.defaults.screen_threshold;
  const required =
    typeof state?.settings.screen_required === "boolean"
      ? state.settings.screen_required
      : state?.defaults.screen_required;
  const value = threshold ?? (current === undefined ? "" : String(current));
  const number = Number(value);
  const valid = value.trim() !== "" && Number.isFinite(number) && number > 0 && number <= 1;
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">Screen</h3>
      {guard.isError ? (
        <LoadError
          what="guard health"
          error={guard.error}
          retry={() => void guard.refetch()}
          retrying={guard.isFetching}
        />
      ) : (
        <p className="text-xs">Guard: {guard.data?.status ?? "loading"}</p>
      )}
      {settings.isError && (
        <LoadError
          what="screen settings"
          error={settings.error}
          retry={() => void settings.refetch()}
          retrying={settings.isFetching}
        />
      )}
      {state && (
        <>
          <form
            className="space-y-2"
            onSubmit={(event) => {
              event.preventDefault();
              if (valid)
                save.mutate(
                  { screen_threshold: number },
                  {
                    onSuccess: () => {
                      setThreshold(null);
                    },
                  },
                );
            }}
          >
            <Label htmlFor="screen-threshold">Screen threshold</Label>
            <div className="flex gap-2">
              <Input
                id="screen-threshold"
                type="number"
                min="0"
                max="1"
                step="any"
                value={value}
                onChange={(event) => {
                  setThreshold(event.target.value);
                }}
              />
              <Button type="submit" disabled={!valid || save.isPending}>
                Save
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              Scores at or above this value are suspect. Enter a number above 0 and at most 1.
            </p>
          </form>
          <div className="flex items-center justify-between gap-3">
            <Label htmlFor="screen-required">Require screening</Label>
            <Switch
              id="screen-required"
              checked={required === true}
              disabled={save.isPending}
              onCheckedChange={(checked) => {
                save.mutate({ screen_required: checked });
              }}
            />
          </div>
          <p className="text-muted-foreground text-xs">
            Block untrusted content when the guard is unavailable.
          </p>
        </>
      )}
      {save.isError && <ActionError action="save screen settings" error={save.error} />}
    </section>
  );
}
