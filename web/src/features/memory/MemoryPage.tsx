/**
 * The Memory page: the user's memories in a list with filters and search,
 * and the open one in full beside it. Read-only; editing comes later.
 */

import { useState } from "react";
import { useParams } from "react-router";

import { MasterDetail } from "@/components/MasterDetail";
import { MemoryDetail } from "@/features/memory/MemoryDetail";
import { MemoryList } from "@/features/memory/MemoryList";
import { noFilters } from "@/features/memory/state";
import type { Filters } from "@/features/memory/state";

export function MemoryPage() {
  const { name } = useParams();
  const [filters, setFilters] = useState<Filters>(noFilters);
  return (
    <MasterDetail
      listLabel="Memories"
      open={name !== undefined}
      list={<MemoryList filters={filters} onChange={setFilters} />}
      detail={
        name === undefined ? (
          <p className="text-muted-foreground m-auto p-4 text-sm">Pick a memory to read it.</p>
        ) : (
          <MemoryDetail key={name} name={name} />
        )
      }
    />
  );
}
