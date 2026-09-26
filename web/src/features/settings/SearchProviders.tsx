/**
 * The search backends: the order the failover chain tries the web providers
 * in, and the health of the sources an agent searches by name.
 */

import { ArrowDown, ArrowUp } from "lucide-react";
import { useState } from "react";

import type { SearchBackendStatus, SearchStatus, SettingsState } from "@/api/types";
import { ActionError, Notice } from "@/components/Notice";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
  moveProvider,
  readOrder,
  searchSettingKeys,
  toggleProvider,
  usageText,
} from "@/features/search";
import type { useSaveSettings } from "@/features/settings/queries";

/** StateBadge shows a backend's state: ready, or why not. */
function StateBadge({ backend }: { backend: SearchBackendStatus }) {
  const ready = backend.state === "ready";
  return (
    <Badge
      variant={ready ? "secondary" : "outline"}
      className={ready ? "" : "text-muted-foreground"}
    >
      {backend.state}
    </Badge>
  );
}

type SaveSettings = ReturnType<typeof useSaveSettings>;

type ProviderOrderProps = { state: SettingsState; status: SearchStatus; save: SaveSettings };

export function ProviderOrder({ state, status, save }: ProviderOrderProps) {
  const stored = readOrder(state);
  const [order, setOrder] = useState(stored);
  const all = state.defaults.search_order;
  // Enabled providers in their order, then the disabled ones in the default order.
  const rows = [...order, ...all.filter((name) => !order.includes(name))];
  const changed = order.join() !== stored.join();
  const backend = (name: string) => status.backends.find((b) => b.name === name);

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate({ [searchSettingKeys.order]: order });
      }}
    >
      <div>
        <h3 className="text-sm font-medium">Web providers</h3>
        <p className="text-muted-foreground text-xs">
          A web search asks one provider: the first here that has its key, quota to spare, and no
          cooldown. One that fails is left alone for a while, longer each time, and the next one
          answers.
        </p>
      </div>
      <ul className="divide-y rounded-md border">
        {rows.map((name) => {
          const enabled = order.includes(name);
          const b = backend(name);
          const index = order.indexOf(name);
          return (
            // One line on a phone: the name truncates and the arrows stay at
            // the end; usage and the probe move to a second line there.
            <li key={name} className="flex items-center gap-3 px-3 py-1.5 sm:py-2">
              <Checkbox
                id={`provider-${name}`}
                checked={enabled}
                onCheckedChange={() => {
                  setOrder(toggleProvider(order, name));
                }}
                aria-label={`Use ${name}`}
              />
              <div className="flex min-w-0 flex-1 flex-col sm:flex-row sm:flex-wrap sm:items-center sm:gap-x-3">
                <div className="flex min-w-0 items-center gap-2 sm:gap-3">
                  <Label
                    htmlFor={`provider-${name}`}
                    title={name}
                    className="block min-w-0 truncate font-mono text-sm sm:min-w-24"
                  >
                    {name}
                  </Label>
                  {b && <StateBadge backend={b} />}
                </div>
                {b && <span className="text-muted-foreground text-xs">{usageText(b)}</span>}
                {b?.probe && (
                  <span
                    className="text-muted-foreground min-w-0 truncate text-xs"
                    title={`${status.searxng_url}: ${b.probe}`}
                  >
                    <span className="font-mono">{status.searxng_url}</span>: {b.probe}
                  </span>
                )}
              </div>
              <div className="flex shrink-0 gap-1">
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={`Move ${name} up`}
                  disabled={!enabled || index === 0}
                  onClick={() => {
                    setOrder(moveProvider(order, name, -1));
                  }}
                >
                  <ArrowUp />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={`Move ${name} down`}
                  disabled={!enabled || index === order.length - 1}
                  onClick={() => {
                    setOrder(moveProvider(order, name, 1));
                  }}
                >
                  <ArrowDown />
                </Button>
              </div>
            </li>
          );
        })}
      </ul>
      <div className="flex gap-2">
        <Button type="submit" variant="outline" disabled={!changed || save.isPending}>
          Save order
        </Button>
        <Button
          type="button"
          variant="ghost"
          disabled={save.isPending}
          onClick={() => {
            setOrder([...all]);
            save.mutate({ [searchSettingKeys.order]: null });
          }}
        >
          Reset
        </Button>
      </div>
      {order.length === 0 && (
        <Notice tone="info">
          With no provider selected, web searches fail. The sources still work.
        </Notice>
      )}
      {save.isError && <ActionError action="save the provider order" error={save.error} />}
    </form>
  );
}

export function Sources({ status }: { status: SearchStatus }) {
  const sources = status.backends.filter((b) => !b.web);
  return (
    <div className="space-y-2">
      <div>
        <h3 className="text-sm font-medium">Sources</h3>
        <p className="text-muted-foreground text-xs">
          An agent searches these by name. They rank their own content better than a web search
          would.
        </p>
      </div>
      <ul className="space-y-1">
        {sources.map((b) => (
          <li key={b.name} className="flex flex-wrap items-center gap-3">
            <span className="min-w-28 font-mono text-sm">{b.name}</span>
            <StateBadge backend={b} />
            {b.bucket && <span className="text-muted-foreground text-xs">{usageText(b)}</span>}
          </li>
        ))}
      </ul>
      <p className="text-muted-foreground text-xs">
        Cached: {status.cached_searches} searches, {status.cached_pages} pages.
      </p>
    </div>
  );
}
