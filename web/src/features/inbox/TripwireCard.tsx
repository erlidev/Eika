/** One tripwire that fired: what happened, what the run is doing, and the two answers. */

import { useState } from "react";
import { Link } from "react-router";

import type { Session, TripwireEntry, TripwireSource } from "@/api/types";
import { Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { useAnswerTripwire } from "@/features/inbox/queries";
import { tripwireFailure, tripwireState, tripwireTitle } from "@/features/inbox/state";
import type { TripwireAnswer } from "@/features/inbox/state";
import { sourceParts } from "@/features/memory/state";
import { formatAgo } from "@/lib/format";

const sourceWords: Record<TripwireSource, string> = {
  user: "you typed it",
  search: "a search result named it",
  content: "only something the agent read named it",
  model: "neither you nor a search named it",
};

export type TripwireCardProps = { entry: TripwireEntry; sessions: Session[] };

export function TripwireCard({ entry, sessions }: TripwireCardProps) {
  const [failure, setFailure] = useState<{ answer: TripwireAnswer; error: unknown } | undefined>();
  const answer = useAnswerTripwire();
  const link = sourceParts(`session:${entry.session_id}`, sessions)[0]?.to;
  const send = (choice: TripwireAnswer) => {
    setFailure(undefined);
    answer.mutate(
      { id: entry.id, answer: choice },
      {
        onError: (error) => {
          setFailure({ answer: choice, error });
        },
      },
    );
  };
  return (
    <article
      aria-label={tripwireTitle[entry.tripwire]}
      className="bg-card space-y-3 rounded-md border p-3 sm:p-4"
    >
      <header className="space-y-1">
        <h2 className="text-lg font-semibold">{tripwireTitle[entry.tripwire]}</h2>
        <p className="text-muted-foreground flex flex-wrap gap-x-1.5 text-xs">
          <span>{formatAgo(entry.at)}</span>
          {link !== undefined && (
            <Link to={link} className="text-primary underline-offset-2 hover:underline">
              Open the session
            </Link>
          )}
        </p>
      </header>
      <p className="text-sm break-words">{entry.summary}</p>
      {entry.host !== undefined && (
        <p className="text-muted-foreground text-xs break-all">
          Destination <span className="text-foreground font-mono">{entry.host}</span>
          {entry.source !== undefined && ` (${sourceWords[entry.source]})`}
        </p>
      )}
      <Notice tone={entry.held && entry.running ? "warning" : "info"}>
        {tripwireState(entry)}
      </Notice>
      {entry.host !== undefined && (
        <p className="text-muted-foreground text-xs">
          &ldquo;This was fine&rdquo; ends the hold and treats the destination as known from now on.
          {!entry.running && " Dismiss closes this without that."}
        </p>
      )}
      {failure !== undefined && (
        <Notice tone="error">{tripwireFailure(failure.answer, failure.error)}</Notice>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          disabled={answer.isPending}
          onClick={() => {
            send("fine");
          }}
        >
          This was fine
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={answer.isPending}
          onClick={() => {
            send("stop");
          }}
        >
          {entry.running ? "Stop the run" : "Dismiss"}
        </Button>
      </div>
    </article>
  );
}
