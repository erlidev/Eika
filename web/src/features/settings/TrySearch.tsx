/** A box to try a search the way an agent would, spending quota like one. */

import { useState } from "react";

import { ActionError } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useSearchStatus, useTrySearch } from "@/features/search";
import { cn } from "@/lib/utils";

export function TrySearch() {
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
      {search.isError && <ActionError action="run the search" error={search.error} />}
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
