/** The list: type and state filters, search, and a row per memory. */

import { Search } from "lucide-react";
import { NavLink } from "react-router";

import type { MemoryItem, MemoryState, MemoryType } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PinMark, StateChip, TypeChip } from "@/features/memory/Chips";
import { useMemories } from "@/features/memory/queries";
import { memoryTypes, noFilters, stateLabel, typeLabel } from "@/features/memory/state";
import type { Filters } from "@/features/memory/state";
import { cn } from "@/lib/utils";

export type MemoryListProps = { filters: Filters; onChange: (filters: Filters) => void };

export function MemoryList({ filters, onChange }: MemoryListProps) {
  const memories = useMemories(filters);
  const filtered =
    filters.type !== "" || filters.state !== "" || filters.pinned || filters.search.trim() !== "";
  return (
    <>
      <Controls filters={filters} onChange={onChange} />
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto">
        {memories.isPending ? (
          <div className="p-3">
            <Notice tone="pending">Loading the memories…</Notice>
          </div>
        ) : memories.isError ? (
          <div className="p-3">
            <LoadError
              what="the memories"
              error={memories.error}
              retry={() => void memories.refetch()}
              retrying={memories.isFetching}
            />
          </div>
        ) : memories.data.items.length === 0 ? (
          <div className="text-muted-foreground space-y-2 p-4 text-sm">
            <p>
              {filtered
                ? "No memory matches."
                : "No memories yet. What Eika learns about you shows here."}
            </p>
            {filtered && (
              <Button
                type="button"
                size="xs"
                variant="outline"
                onClick={() => {
                  onChange(noFilters);
                }}
              >
                Clear filters
              </Button>
            )}
          </div>
        ) : (
          <>
            <ul>
              {memories.data.items.map((m) => (
                <li key={m.name}>
                  <Row memory={m} />
                </li>
              ))}
            </ul>
            {memories.hasNextPage && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="m-2"
                disabled={memories.isFetchingNextPage}
                onClick={() => void memories.fetchNextPage()}
              >
                {memories.isFetchingNextPage ? "Loading…" : "Show more"}
              </Button>
            )}
          </>
        )}
      </div>
    </>
  );
}

type ControlsProps = { filters: Filters; onChange: (filters: Filters) => void };

const states: readonly MemoryState[] = ["active", "pending", "archived"];

function Controls({ filters, onChange }: ControlsProps) {
  const set = (change: Partial<Filters>) => {
    onChange({ ...filters, ...change });
  };
  return (
    <div className="space-y-2 border-b px-3 py-2">
      <div className="relative">
        <Search
          aria-hidden
          className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2"
        />
        <Input
          type="search"
          aria-label="Search memories"
          placeholder="Search memories"
          maxLength={200}
          className="pl-8"
          value={filters.search}
          onChange={(e) => {
            set({ search: e.target.value });
          }}
        />
      </div>
      <Chips label="Type">
        <FilterChip
          pressed={filters.type === ""}
          onClick={() => {
            set({ type: "" });
          }}
        >
          All types
        </FilterChip>
        {memoryTypes.map((t: MemoryType) => (
          <FilterChip
            key={t}
            pressed={filters.type === t}
            onClick={() => {
              set({ type: t });
            }}
          >
            {typeLabel[t]}
          </FilterChip>
        ))}
      </Chips>
      <Chips label="State">
        <FilterChip
          pressed={filters.state === ""}
          onClick={() => {
            set({ state: "" });
          }}
        >
          Any state
        </FilterChip>
        {states.map((s) => (
          <FilterChip
            key={s}
            pressed={filters.state === s}
            onClick={() => {
              set({ state: s });
            }}
          >
            {stateLabel[s]}
          </FilterChip>
        ))}
        <FilterChip
          pressed={filters.pinned}
          onClick={() => {
            set({ pinned: !filters.pinned });
          }}
        >
          Pinned
        </FilterChip>
      </Chips>
    </div>
  );
}

function Chips({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div role="group" aria-label={label} className="flex flex-wrap gap-1.5">
      {children}
    </div>
  );
}

type FilterChipProps = { pressed: boolean; onClick: () => void; children: React.ReactNode };

function FilterChip({ pressed, onClick, children }: FilterChipProps) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      className={cn(
        "hover:bg-accent focus-visible:ring-ring shrink-0 rounded-full border px-2.5 py-0.5 text-xs transition-colors focus-visible:ring-1 focus-visible:outline-none",
        pressed && "bg-accent font-medium",
      )}
      onClick={onClick}
    >
      {children}
    </button>
  );
}

/** Row is one memory: its name, type, pin and state, and its description. */
function Row({ memory }: { memory: MemoryItem }) {
  return (
    <NavLink
      to={`/memory/${encodeURIComponent(memory.name)}`}
      className="hover:bg-accent aria-[current=page]:bg-accent focus-visible:ring-ring flex min-h-11 min-w-0 flex-col justify-center gap-0.5 px-3 py-1.5 transition-colors focus-visible:ring-1 focus-visible:outline-none"
    >
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="min-w-0 truncate text-sm font-medium">{memory.name}</span>
        <span className="ml-auto flex shrink-0 items-center gap-1">
          {memory.pinned && <PinMark />}
          <StateChip state={memory.state} />
          <TypeChip type={memory.type} />
        </span>
      </span>
      <span className="text-muted-foreground min-w-0 truncate text-xs">{memory.description}</span>
    </NavLink>
  );
}
