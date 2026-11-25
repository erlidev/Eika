/** One pending memory: where it came from, what was flagged, and the answers. */

import { useState } from "react";
import { Link } from "react-router";

import type { EditMemoryRequest, Memory, MemoryType, Session } from "@/api/types";
import { Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { TypeChip } from "@/features/memory/Chips";
import { memoryTypes, sourceParts, typeLabel } from "@/features/memory/state";
import { answerFailure } from "@/features/inbox/state";
import type { Answer } from "@/features/inbox/state";
import { useAccept, useReject } from "@/features/inbox/queries";
import { formatAgo } from "@/lib/format";
import { segments } from "@/lib/segments";

/** Flagged shows a field's text with the screened spans marked. */
function Flagged({ memory, field }: { memory: Memory; field: "description" | "body" }) {
  const text = memory[field];
  return (
    <>
      {segments(text, field, memory.screen?.spans ?? []).map((part, i) =>
        part.flagged ? (
          <mark key={i} className="bg-warning/20 text-foreground rounded-md px-0.5">
            {part.text}
          </mark>
        ) : (
          <span key={i}>{part.text}</span>
        ),
      )}
    </>
  );
}

export type MemoryCardProps = { memory: Memory; sessions: Session[] };

export function MemoryCard({ memory, sessions }: MemoryCardProps) {
  const [editing, setEditing] = useState(false);
  const [failure, setFailure] = useState<{ answer: Answer; error: unknown } | undefined>();
  const accept = useAccept();
  const reject = useReject();
  const busy = accept.isPending || reject.isPending;
  const spans = memory.screen?.spans ?? [];
  const flaggedText = spans.length > 0 || memory.screen?.unscanned === true;

  const send = (answer: Answer, edit?: EditMemoryRequest) => {
    setFailure(undefined);
    const done = {
      onError: (error: unknown) => {
        setFailure({ answer, error });
      },
    };
    if (answer === "accept") {
      accept.mutate({ name: memory.name, edit: edit ?? { content_sha: memory.content_sha } }, done);
    } else {
      reject.mutate({ name: memory.name, contentSha: memory.content_sha }, done);
    }
  };

  return (
    <article aria-label={memory.name} className="bg-card space-y-3 rounded-md border p-3 sm:p-4">
      <header className="space-y-1">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <h2 className="text-lg font-semibold break-all">{memory.name}</h2>
          <TypeChip type={memory.type} />
        </div>
        <p className="text-muted-foreground flex flex-wrap gap-x-1.5 text-xs">
          <span>From</span>
          {memory.source === "" ? (
            <span>an unknown source</span>
          ) : (
            sourceParts(memory.source, sessions).map((part, i) =>
              part.to === undefined ? (
                <span key={i} className="break-all">
                  {part.text}
                </span>
              ) : (
                <Link
                  key={i}
                  to={part.to}
                  className="text-primary break-all underline-offset-2 hover:underline"
                >
                  {part.text}
                </Link>
              ),
            )
          )}
          <span>· {formatAgo(memory.updated)}</span>
        </p>
      </header>
      {flaggedText && (
        <Notice tone="warning">
          {spans.length > 0
            ? "The injection screen flagged the highlighted text. Read it before you accept."
            : "The injection screen could not check this memory."}
        </Notice>
      )}
      {editing ? (
        <EditForm
          key={memory.content_sha}
          memory={memory}
          busy={busy}
          onCancel={() => {
            setEditing(false);
          }}
          onSave={(edit) => {
            send("accept", edit);
          }}
        />
      ) : (
        <div className="space-y-2">
          <p className="text-sm break-words">
            <Flagged memory={memory} field="description" />
          </p>
          {memory.body.trim() !== "" && (
            <pre className="bg-muted/50 rounded-md p-2 font-mono text-xs break-words whitespace-pre-wrap">
              <Flagged memory={memory} field="body" />
            </pre>
          )}
          {memory.expires !== "" && (
            <p className="text-muted-foreground text-xs">Expires {memory.expires}</p>
          )}
        </div>
      )}
      {failure !== undefined && (
        <Notice tone="error">{answerFailure(failure.answer, failure.error)}</Notice>
      )}
      {!editing && (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            disabled={busy}
            onClick={() => {
              send("accept");
            }}
          >
            {accept.isPending ? "Accepting…" : "Accept"}
          </Button>
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => {
              setEditing(true);
            }}
          >
            Edit
          </Button>
          <Button
            type="button"
            variant="ghost"
            className="ml-auto"
            disabled={busy}
            onClick={() => {
              send("reject");
            }}
          >
            {reject.isPending ? "Rejecting…" : "Reject"}
          </Button>
        </div>
      )}
    </article>
  );
}

type EditFormProps = {
  memory: Memory;
  busy: boolean;
  onCancel: () => void;
  onSave: (edit: EditMemoryRequest) => void;
};

/** EditForm changes what it must before accepting: only the fields that differ are sent. */
function EditForm({ memory, busy, onCancel, onSave }: EditFormProps) {
  const [description, setDescription] = useState(memory.description);
  const [type, setType] = useState<MemoryType>(memory.type);
  const [body, setBody] = useState(memory.body);
  const [expires, setExpires] = useState(memory.expires);
  const save = () => {
    onSave({
      content_sha: memory.content_sha,
      ...(description !== memory.description ? { description } : {}),
      ...(type !== memory.type ? { type } : {}),
      ...(body !== memory.body ? { body } : {}),
      ...(expires !== memory.expires ? { expires } : {}),
    });
  };
  return (
    <form
      className="space-y-2"
      onSubmit={(e) => {
        e.preventDefault();
        save();
      }}
    >
      <label className="block space-y-1 text-xs">
        <span className="text-muted-foreground">Description</span>
        <Input
          value={description}
          maxLength={200}
          onChange={(e) => {
            setDescription(e.target.value);
          }}
        />
      </label>
      <div className="grid grid-cols-2 gap-2">
        <label className="block space-y-1 text-xs">
          <span className="text-muted-foreground">Type</span>
          <select
            value={type}
            className="border-input bg-background h-8 w-full rounded-md border px-2 text-sm"
            onChange={(e) => {
              setType(e.target.value as MemoryType);
            }}
          >
            {memoryTypes.map((t) => (
              <option key={t} value={t}>
                {typeLabel[t]}
              </option>
            ))}
          </select>
        </label>
        <label className="block space-y-1 text-xs">
          <span className="text-muted-foreground">Expires</span>
          <Input
            type="date"
            value={expires}
            onChange={(e) => {
              setExpires(e.target.value);
            }}
          />
        </label>
      </div>
      <label className="block space-y-1 text-xs">
        <span className="text-muted-foreground">Body</span>
        <Textarea
          value={body}
          className="font-mono text-xs"
          onChange={(e) => {
            setBody(e.target.value);
          }}
        />
      </label>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={busy}>
          {busy ? "Accepting…" : "Save and accept"}
        </Button>
        <Button type="button" variant="ghost" disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
