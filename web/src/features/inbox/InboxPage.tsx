/**
 * The Inbox: what waits for the user's answer. For now that is pending
 * memories, one card each, newest first in a column capped at 40rem.
 */

import { Inbox } from "lucide-react";

import { LoadError, Notice } from "@/components/Notice";
import { MemoryCard } from "@/features/inbox/MemoryCard";
import { listLimit, useInboxMemories } from "@/features/inbox/queries";
import { useSources } from "@/features/memory/queries";

export function InboxPage() {
  const inbox = useInboxMemories();
  const sessions = useSources();
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-[40rem] space-y-3 p-3 sm:p-4">
        {inbox.isPending ? (
          <Notice tone="pending">Loading the inbox…</Notice>
        ) : inbox.isError && inbox.data === undefined ? (
          <LoadError
            what="the inbox"
            error={inbox.error}
            retry={() => void inbox.refetch()}
            retrying={inbox.isFetching}
          />
        ) : (
          <>
            {inbox.isError && (
              <LoadError
                what="the inbox"
                error={inbox.error}
                retry={() => void inbox.refetch()}
                retrying={inbox.isFetching}
              />
            )}
            {inbox.data.memories.length === 0 ? (
              <div className="text-muted-foreground flex flex-col items-center gap-2 py-12 text-center text-sm">
                <Inbox aria-hidden className="size-4" />
                <p>Nothing is waiting for you.</p>
                <p className="max-w-xs text-xs">
                  A memory Eika is unsure about shows up here for you to accept, edit or reject.
                </p>
              </div>
            ) : (
              <>
                <p className="text-muted-foreground text-xs">
                  {inbox.data.pending === 1
                    ? "1 memory is waiting for your answer. It is not used until you accept it."
                    : `${String(inbox.data.pending)} memories are waiting for your answer. They are not used until you accept them.`}
                </p>
                <ul className="space-y-3">
                  {inbox.data.memories.map((memory) => (
                    <li key={memory.name}>
                      <MemoryCard memory={memory} sessions={sessions.data ?? []} />
                    </li>
                  ))}
                </ul>
                {inbox.data.pending > listLimit && (
                  <p className="text-muted-foreground text-xs">
                    Showing the newest {listLimit}. Answer some to see the rest.
                  </p>
                )}
              </>
            )}
          </>
        )}
      </div>
    </div>
  );
}
