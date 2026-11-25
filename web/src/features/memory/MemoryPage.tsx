/**
 * The Memory page and its second tab, Instructions. Memory is the user's
 * memories in a list with filters and search and the open one in an editor
 * beside it. Instructions holds "About me" and the rules, the same editor the
 * settings dialog shows, at /memory?tab=instructions.
 */

import { useState } from "react";
import { NavLink, useParams, useSearchParams } from "react-router";

import { MasterDetail } from "@/components/MasterDetail";
import { MemoryDetail } from "@/features/memory/MemoryDetail";
import { MemoryList } from "@/features/memory/MemoryList";
import { noFilters } from "@/features/memory/state";
import type { Filters } from "@/features/memory/state";
import { InstructionsSettings } from "@/features/settings/InstructionsSettings";
import { cn } from "@/lib/utils";

/** instructionsQuery selects the Instructions tab; a query cannot collide with a memory's name. */
const instructionsQuery = "tab=instructions";

const tab =
  "hover:bg-accent focus-visible:ring-ring rounded-md px-2.5 py-1 text-sm transition-colors focus-visible:ring-1 focus-visible:outline-none";

export function MemoryPage() {
  const { name } = useParams();
  const [filters, setFilters] = useState<Filters>(noFilters);
  const [search] = useSearchParams();
  const instructions = name === undefined && search.get("tab") === "instructions";
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <nav aria-label="Memory sections" className="flex gap-1 border-b px-3 py-1.5">
        <NavLink
          to="/memory"
          end
          className={cn(tab, !instructions && "bg-accent font-medium")}
          aria-current={instructions ? undefined : "page"}
        >
          Memory
        </NavLink>
        <NavLink
          to={`/memory?${instructionsQuery}`}
          className={cn(tab, instructions && "bg-accent font-medium")}
          aria-current={instructions ? "page" : undefined}
        >
          Instructions
        </NavLink>
      </nav>
      {instructions ? (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto max-w-3xl p-4">
            <InstructionsSettings />
          </div>
        </div>
      ) : (
        <MasterDetail
          listLabel="Memories"
          open={name !== undefined}
          list={<MemoryList filters={filters} onChange={setFilters} />}
          detail={
            name === undefined ? (
              <p className="text-muted-foreground m-auto p-4 text-sm">Pick a memory to edit it.</p>
            ) : (
              <MemoryDetail key={name} name={name} />
            )
          }
        />
      )}
    </div>
  );
}
