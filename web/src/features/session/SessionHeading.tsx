/**
 * The session header's title, which is also where the session is renamed:
 * a click on it swaps it for a field. An archived session says so beside the
 * title, with the way back out of the archive.
 */

import { ArchiveRestore, Pencil } from "lucide-react";
import { useState } from "react";

import type { Session } from "@/api/types";
import { RenameField } from "@/components/RenameField";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useUpdateSession } from "@/features/sessions";
import { failureText } from "@/lib/failure";

export type SessionHeadingProps = {
  /** session is absent while it loads, when the heading is a placeholder. */
  session: Session | undefined;
};

export function SessionHeading({ session }: SessionHeadingProps) {
  const update = useUpdateSession();
  const [renaming, setRenaming] = useState(false);
  if (session === undefined) {
    return <h2 className="truncate text-sm font-semibold">Session</h2>;
  }
  return (
    <>
      <h2 className="flex min-w-0 items-center text-sm font-semibold">
        {renaming ? (
          <RenameField
            value={session.title}
            label="Session title"
            className="w-72 max-w-full text-sm font-semibold md:text-sm"
            onSubmit={(title) => {
              update.mutate({ id: session.id, changes: { title } });
            }}
            onDone={() => {
              setRenaming(false);
            }}
          />
        ) : (
          <button
            type="button"
            title="Rename the session"
            onClick={() => {
              setRenaming(true);
            }}
            className="group hover:bg-accent focus-visible:ring-ring -mx-1 flex min-w-0 items-center gap-1.5 rounded-md px-1 transition-colors focus-visible:ring-1 focus-visible:outline-none"
          >
            <span className="truncate">{session.title}</span>
            <Pencil
              aria-hidden
              className="text-muted-foreground size-3.5 shrink-0 opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 max-sm:hidden"
            />
          </button>
        )}
      </h2>
      {session.archived && (
        <Badge variant="outline" className="h-5 shrink-0 gap-1 px-1.5 text-2xs">
          Archived
          <Button
            size="icon-xs"
            variant="ghost"
            className="-mr-1 size-4"
            aria-label="Unarchive"
            title="Unarchive"
            onClick={() => {
              update.mutate({ id: session.id, changes: { archived: false } });
            }}
          >
            <ArchiveRestore aria-hidden className="size-3" />
          </Button>
        </Badge>
      )}
      {update.isError && (
        <span role="alert" className="text-destructive truncate text-xs">
          {failureText("change the session", update.error)}
        </span>
      )}
    </>
  );
}
