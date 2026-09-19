/**
 * The Search tab: which web providers the failover chain tries and in what
 * order, their API keys, the quotas that keep them inside a free tier, the
 * health of every backend, and a box to try a search the way an agent would.
 */

import { ArrowDown, ArrowUp } from "lucide-react";
import { useState } from "react";

import type { SearchBackendStatus, SearchLimit, SearchStatus, SettingsState } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import {
  keyLabels,
  type LimitField,
  limitProblem,
  maxLimit,
  moveProvider,
  parseLimit,
  readLimits,
  readOrder,
  searchSettingKeys,
  toggleProvider,
  usageText,
  useSaveSearchKey,
  useSearchStatus,
  useTrySearch,
} from "@/features/search";
import { useSaveSettings, useSettings } from "@/features/settings/queries";
import { cn } from "@/lib/utils";

export function SearchSettings() {
  const settings = useSettings();
  const status = useSearchStatus();
  // The saves live here, above the forms they serve, so that a form that
  // remounts on the value it saved still shows how the save went.
  const saveOrder = useSaveSettings();
  const saveLimits = useSaveSettings();
  if (settings.isError) {
    return (
      <LoadError
        what="the settings"
        error={settings.error}
        retrying={settings.isFetching}
        retry={() => void settings.refetch()}
      />
    );
  }
  if (status.isError) {
    return (
      <LoadError
        what="the search status"
        error={status.error}
        retrying={status.isFetching}
        retry={() => void status.refetch()}
      />
    );
  }
  if (!settings.data || !status.data) return <Notice tone="pending">Loading search…</Notice>;
  return (
    <div className="space-y-6">
      {/* Keyed by the stored value, so a form starts from what is stored now
          and a save elsewhere leaves its unsaved edits alone. */}
      <ProviderOrder
        key={`order-${JSON.stringify(settings.data.settings[searchSettingKeys.order] ?? null)}`}
        state={settings.data}
        status={status.data}
        save={saveOrder}
      />
      <Separator />
      <SearchKeys status={status.data} />
      <Separator />
      <Quotas
        key={`limits-${JSON.stringify(settings.data.settings[searchSettingKeys.limits] ?? null)}`}
        state={settings.data}
        save={saveLimits}
      />
      <Separator />
      <Sources status={status.data} />
      <Separator />
      <TrySearch />
    </div>
  );
}

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

function ProviderOrder({ state, status, save }: ProviderOrderProps) {
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
            <li key={name} className="flex flex-wrap items-center gap-3 px-3 py-2">
              <Checkbox
                id={`provider-${name}`}
                checked={enabled}
                onCheckedChange={() => {
                  setOrder(toggleProvider(order, name));
                }}
                aria-label={`Use ${name}`}
              />
              <Label htmlFor={`provider-${name}`} className="min-w-24 font-mono text-sm">
                {name}
              </Label>
              {b && <StateBadge backend={b} />}
              {b && <span className="text-muted-foreground text-xs">{usageText(b)}</span>}
              {b?.probe && (
                <span className="text-muted-foreground text-xs">
                  {status.searxng_url}: {b.probe}
                </span>
              )}
              <div className="ml-auto flex gap-1">
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
      {save.isError && <Notice tone="error">{save.error.message}</Notice>}
    </form>
  );
}

function SearchKeys({ status }: { status: SearchStatus }) {
  return (
    <div className="space-y-3">
      <div>
        <h3 className="text-sm font-medium">API keys</h3>
        <p className="text-muted-foreground text-xs">
          Sealed in the harness and never shown again. Exa, Tavily, and Brave are used only when
          they have a key. A GitHub token enables code search and raises GitHub&apos;s rate limit
          for the other sources and for reading GitHub pages.
        </p>
      </div>
      {status.keys.map((key) => (
        <KeyForm key={key.name} name={key.name} set={key.set} hint={key.hint} />
      ))}
    </div>
  );
}

function KeyForm({ name, set, hint }: { name: string; set: boolean; hint?: string | undefined }) {
  const save = useSaveSearchKey();
  const [key, setKey] = useState("");
  const id = `search-key-${name}`;
  return (
    <form
      className="space-y-1"
      onSubmit={(e) => {
        e.preventDefault();
        if (key.trim() === "") return;
        save.mutate(
          { name, key: key.trim() },
          {
            onSuccess: () => {
              setKey("");
            },
          },
        );
      }}
    >
      <Label htmlFor={id} className="text-xs">
        {keyLabels[name] ?? name}
      </Label>
      <div className="flex gap-2">
        <Input
          id={id}
          type="password"
          autoComplete="off"
          value={key}
          placeholder={
            set ? `Stored${hint ? `, ends in ${hint}` : ""}. Type to replace.` : "Not set"
          }
          onChange={(e) => {
            setKey(e.target.value);
          }}
        />
        <Button type="submit" variant="outline" disabled={key.trim() === "" || save.isPending}>
          Save
        </Button>
        <Button
          type="button"
          variant="ghost"
          disabled={!set || save.isPending}
          onClick={() => {
            save.mutate({ name, key: "" });
          }}
        >
          Remove
        </Button>
      </div>
      {save.isError && <Notice tone="error">{save.error.message}</Notice>}
    </form>
  );
}

type QuotaFields = { day: LimitField; month: LimitField };

function Quotas({ state, save }: { state: SettingsState; save: SaveSettings }) {
  const stored = readLimits(state);
  const [fields, setFields] = useState<Record<string, QuotaFields>>(() =>
    Object.fromEntries(
      Object.entries(stored).map(([bucket, l]) => [
        bucket,
        {
          day: { text: l.day ? String(l.day) : "" },
          month: { text: l.month ? String(l.month) : "" },
        },
      ]),
    ),
  );
  const buckets = Object.keys(stored).sort();
  const invalid = Object.values(fields).some(
    (f) => limitProblem(f.day) !== null || limitProblem(f.month) !== null,
  );

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (invalid) return;
        const limits: Record<string, SearchLimit> = {};
        for (const [bucket, f] of Object.entries(fields)) {
          const day = parseLimit(f.day.text);
          const month = parseLimit(f.month.text);
          limits[bucket] = {
            ...(day === undefined ? {} : { day }),
            ...(month === undefined ? {} : { month }),
          };
        }
        save.mutate({ [searchSettingKeys.limits]: limits });
      }}
    >
      <div>
        <h3 className="text-sm font-medium">Quotas</h3>
        <p className="text-muted-foreground text-xs">
          Requests a provider may take per UTC day or month before the chain skips it, to stay
          inside a free tier. Enter a whole number from 1 to {maxLimit.toLocaleString("en-US")}, or
          leave a field empty for unlimited. Counts survive a restart.
        </p>
      </div>
      <div className="grid grid-cols-[auto_1fr_1fr] items-start gap-x-3 gap-y-2">
        <span />
        <span className="text-muted-foreground text-xs">Per day</span>
        <span className="text-muted-foreground text-xs">Per month</span>
        {buckets.map((bucket) => (
          <QuotaRow
            key={bucket}
            bucket={bucket}
            value={fields[bucket] ?? { day: { text: "" }, month: { text: "" } }}
            onChange={(value) => {
              setFields({ ...fields, [bucket]: value });
            }}
          />
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" variant="outline" disabled={invalid || save.isPending}>
          Save quotas
        </Button>
        {invalid && (
          <span className="text-destructive text-xs">Fix the quotas marked above to save.</span>
        )}
      </div>
      {save.isSuccess && <Notice tone="success">Saved. The next search uses these quotas.</Notice>}
      {save.isError && <Notice tone="error">{save.error.message}</Notice>}
    </form>
  );
}

type QuotaRowProps = {
  bucket: string;
  value: QuotaFields;
  onChange: (value: QuotaFields) => void;
};

function QuotaRow({ bucket, value, onChange }: QuotaRowProps) {
  return (
    <>
      <span className="pt-2 font-mono text-sm">{bucket}</span>
      <QuotaInput
        label={`${bucket} per day`}
        id={`quota-${bucket}-day`}
        field={value.day}
        onChange={(day) => {
          onChange({ ...value, day });
        }}
      />
      <QuotaInput
        label={`${bucket} per month`}
        id={`quota-${bucket}-month`}
        field={value.month}
        onChange={(month) => {
          onChange({ ...value, month });
        }}
      />
    </>
  );
}

type QuotaInputProps = {
  label: string;
  id: string;
  field: LimitField;
  onChange: (field: LimitField) => void;
};

/** QuotaInput is one quota field with its problem, if any, right below it. */
function QuotaInput({ label, id, field, onChange }: QuotaInputProps) {
  const problem = limitProblem(field);
  return (
    <div className="space-y-1">
      <Input
        id={id}
        aria-label={label}
        type="number"
        inputMode="numeric"
        min={1}
        max={maxLimit}
        step={1}
        value={field.text}
        placeholder="Unlimited"
        aria-invalid={problem !== null}
        aria-describedby={problem ? `${id}-problem` : undefined}
        onChange={(e) => {
          onChange({ text: e.target.value, badInput: e.target.validity.badInput });
        }}
      />
      {problem && (
        <p id={`${id}-problem`} className="text-destructive text-xs">
          {problem}
        </p>
      )}
    </div>
  );
}

function Sources({ status }: { status: SearchStatus }) {
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

function TrySearch() {
  const search = useTrySearch();
  const status = useSearchStatus();
  const [query, setQuery] = useState("");
  const [source, setSource] = useState("web");
  const sources = [
    "web",
    ...(status.data?.backends.filter((b) => !b.web).map((b) => b.name) ?? []),
  ];

  return (
    <form
      className="space-y-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (query.trim() === "") return;
        search.mutate({ query: query.trim(), source });
      }}
    >
      <div>
        <h3 className="text-sm font-medium">Try a search</h3>
        <p className="text-muted-foreground text-xs">
          Runs exactly what an agent&apos;s web_search runs, and spends quota like one.
        </p>
      </div>
      <div className="flex gap-2">
        <Input
          aria-label="Query"
          value={query}
          placeholder="tokio runtime"
          onChange={(e) => {
            setQuery(e.target.value);
          }}
        />
        <Select value={source} onValueChange={setSource}>
          <SelectTrigger aria-label="Source" className="w-40">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {sources.map((name) => (
              <SelectItem key={name} value={name}>
                {name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button type="submit" variant="outline" disabled={query.trim() === "" || search.isPending}>
          Search
        </Button>
      </div>
      {search.isError && <Notice tone="error">{search.error.message}</Notice>}
      {search.data && (
        <div className="space-y-1">
          <p className="text-muted-foreground text-xs">
            {search.data.details.providers?.length
              ? `Answered by ${search.data.details.providers.join(", ")}`
              : search.data.details.source}
            {search.data.details.cached ? " (cached)" : ""} in {search.data.details.ms} ms.
          </p>
          <pre
            className={cn(
              "bg-muted/50 max-h-72 overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap",
              search.data.is_error && "text-destructive",
            )}
          >
            {search.data.text}
          </pre>
        </div>
      )}
    </form>
  );
}
