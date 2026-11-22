/** The open memory in full: its fields, why it waits or was archived, its source, and its body. */

import { ArrowLeft } from "lucide-react";
import { Link } from "react-router";

import { ApiError } from "@/api/client";
import type { Memory } from "@/api/types";
import { Markdown } from "@/components/Markdown";
import { LoadError, Notice } from "@/components/Notice";
import { PinMark, StateChip, TypeChip } from "@/features/memory/Chips";
import { useMemory, useSources } from "@/features/memory/queries";
import { sourceParts } from "@/features/memory/state";
import { formatAgo } from "@/lib/format";

/** BackLink returns to the list; on a screen that shows both, there is nothing to return to. */
function BackLink() {
  return (
    <Link
      to="/memory"
      className="touch-link text-muted-foreground hover:text-foreground flex w-fit items-center gap-1 text-xs transition-colors lg:hidden"
    >
      <ArrowLeft aria-hidden className="size-3.5" />
      Memory
    </Link>
  );
}

export function MemoryDetail({ name }: { name: string }) {
  const memory = useMemory(name);
  if (memory.isPending) {
    return (
      <div className="space-y-2 p-3">
        <BackLink />
        <Notice tone="pending">Loading the memory…</Notice>
      </div>
    );
  }
  if (memory.isError) {
    const gone = memory.error instanceof ApiError && memory.error.status === 404;
    return (
      <div className="space-y-2 p-3">
        <BackLink />
        {gone ? (
          <Notice>This memory no longer exists.</Notice>
        ) : (
          <LoadError
            what="the memory"
            error={memory.error}
            retry={() => void memory.refetch()}
            retrying={memory.isFetching}
          />
        )}
      </div>
    );
  }
  return <Body memory={memory.data} />;
}

function Body({ memory }: { memory: Memory }) {
  const sessions = useSources();
  const spans = memory.screen?.spans ?? [];
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <article className="mx-auto max-w-3xl space-y-3 p-4">
        <BackLink />
        <header className="space-y-1.5">
          <h2 className="text-lg font-semibold break-words">{memory.name}</h2>
          <div className="flex flex-wrap gap-1.5">
            <TypeChip type={memory.type} />
            <StateChip state={memory.state} />
            {memory.pinned && <PinMark />}
          </div>
          <p className="text-sm break-words">{memory.description}</p>
        </header>
        {memory.state === "pending" && (
          <Notice tone="warning">
            This memory is waiting for review and is not used yet.
            {memory.screen?.unscanned === true && " It could not be screened."}
            {spans.length > 0 && (
              <ul className="mt-1 space-y-0.5">
                {spans.map((s) => (
                  <li key={`${s.field}-${String(s.start)}`}>
                    Flagged in the {s.field}: <q className="font-mono">{s.text}</q>
                  </li>
                ))}
              </ul>
            )}
          </Notice>
        )}
        {memory.state === "archived" && (
          <Notice>Archived{memory.archive_reason ? `: ${memory.archive_reason}` : "."}</Notice>
        )}
        <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
          <dt>Updated</dt>
          <dd>{formatAgo(memory.updated)}</dd>
          <dt>Created</dt>
          <dd>{formatAgo(memory.created)}</dd>
          {memory.expires !== "" && (
            <>
              <dt>Expires</dt>
              <dd>{memory.expires}</dd>
            </>
          )}
          {memory.source !== "" && (
            <>
              <dt>Source</dt>
              <dd className="flex flex-wrap gap-x-2 break-all">
                {sourceParts(memory.source, sessions.data ?? []).map((part, i) =>
                  part.to === undefined ? (
                    <span key={i}>{part.text}</span>
                  ) : (
                    <Link
                      key={i}
                      to={part.to}
                      className="text-primary underline-offset-2 hover:underline"
                    >
                      {part.text}
                    </Link>
                  ),
                )}
              </dd>
            </>
          )}
        </dl>
        <section aria-label="Body" className="border-t pt-3">
          {memory.body.trim() === "" ? (
            <p className="text-muted-foreground text-sm">No body.</p>
          ) : (
            <Markdown>{memory.body}</Markdown>
          )}
        </section>
      </article>
    </div>
  );
}
