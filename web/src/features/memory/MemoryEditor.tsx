/**
 * The editor of one memory: its frontmatter as a form, its body in the shared
 * MarkdownField, and Pin, Archive and Purge. A save carries the content_sha the
 * draft was made from; when the memory has changed since (409) the current
 * version is shown beside the draft and the user chooses which to keep.
 */

import { useState } from "react";
import { useNavigate } from "react-router";

import { getMemory } from "@/api/routes";
import type { Memory, MemoryType } from "@/api/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ActionError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  useArchiveMemory,
  useEditMemory,
  usePurgeMemory,
  useSetPinned,
} from "@/features/memory/queries";
import {
  bodyLimit,
  byteLength,
  changesOf,
  draftOf,
  draftProblems,
  isStale,
  isUnavailable,
  memoryTypes,
  typeLabel,
} from "@/features/memory/state";
import type { Draft } from "@/features/memory/state";
import { MarkdownField } from "@/features/session/MarkdownField";
import { useMediaQuery } from "@/lib/useNarrow";
import { cn } from "@/lib/utils";

/** Outcome is what the last action said, kept until the next one. */
type Outcome =
  | { kind: "saved" }
  | { kind: "pending" }
  | { kind: "pinned" | "unpinned" | "archived" }
  | { kind: "failed"; action: string; error: unknown };

export function MemoryEditor({ memory }: { memory: Memory }) {
  const navigate = useNavigate();
  const touch = useMediaQuery("(pointer: coarse)");
  // base is the version the draft was made from; its sha is what a change sends.
  const [base, setBase] = useState(memory);
  const [draft, setDraft] = useState<Draft>(draftOf(memory));
  const [seen, setSeen] = useState(memory);
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [conflict, setConflict] = useState<Memory | null>(null);
  const [purging, setPurging] = useState(false);
  const edit = useEditMemory(memory.name);
  const pin = useSetPinned(memory.name);
  const archive = useArchiveMemory(memory.name);
  const purge = usePurgeMemory(memory.name);

  const changes = changesOf(base, draft);
  // A newer stored memory, from a refetch, replaces an untouched draft only:
  // a draft is never overwritten, and a save of it meets the 409 instead.
  if (memory !== seen) {
    setSeen(memory);
    if (changes === null && conflict === null) {
      setBase(memory);
      setDraft(draftOf(memory));
    }
  }
  const problems = draftProblems(draft);
  const invalid = problems.description !== "" || problems.body !== "";
  const busy = edit.isPending || pin.isPending || archive.isPending || purge.isPending;

  const adopt = (next: Memory) => {
    // The draft keeps its edits over a change that was not an edit.
    if (changes === null) setDraft(draftOf(next));
    setBase(next);
    setConflict(null);
  };
  /** failed shows an error; a stale precondition instead fetches the version that won. */
  const failed = (action: string, error: unknown) => {
    if (!isStale(error)) {
      setOutcome({ kind: "failed", action, error });
      return;
    }
    setOutcome(null);
    getMemory(memory.name).then(setConflict, (e: unknown) => {
      setOutcome({ kind: "failed", action: "read the current memory", error: e });
    });
  };

  const save = () => {
    if (changes === null || invalid || busy) return;
    setOutcome(null);
    edit.mutate(changes, {
      onSuccess: (saved) => {
        setBase(saved);
        setDraft(draftOf(saved));
        setConflict(null);
        setOutcome({
          kind: saved.state === "pending" && base.state !== "pending" ? "pending" : "saved",
        });
      },
      onError: (e) => {
        failed("save the memory", e);
      },
    });
  };
  const setPinned = (pinned: boolean) => {
    setOutcome(null);
    pin.mutate(
      { pinned, contentSha: base.content_sha },
      {
        onSuccess: (saved) => {
          adopt(saved);
          setOutcome({ kind: pinned ? "pinned" : "unpinned" });
        },
        onError: (e) => {
          failed(pinned ? "pin the memory" : "unpin the memory", e);
        },
      },
    );
  };
  const archiveIt = () => {
    setOutcome(null);
    archive.mutate(base.content_sha, {
      onSuccess: (saved) => {
        adopt(saved);
        setOutcome({ kind: "archived" });
      },
      onError: (e) => {
        failed("archive the memory", e);
      },
    });
  };
  const purgeIt = () => {
    setOutcome(null);
    purge.mutate(base.content_sha, {
      onSuccess: () => {
        void navigate("/memory");
      },
      onError: (e) => {
        failed("purge the memory", e);
      },
    });
  };

  const set = (patch: Partial<Draft>) => {
    setDraft({ ...draft, ...patch });
    setOutcome(null);
  };

  return (
    <form
      aria-label="Edit memory"
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        save();
      }}
    >
      {conflict !== null && (
        <Conflict
          current={conflict}
          onReapply={() => {
            setBase(conflict);
            setConflict(null);
          }}
          onUseCurrent={() => {
            setBase(conflict);
            setDraft(draftOf(conflict));
            setConflict(null);
          }}
        />
      )}
      <Result outcome={outcome} state={base.state} />
      <div className="space-y-1.5">
        <Label htmlFor="memory-description">Description</Label>
        <Input
          id="memory-description"
          value={draft.description}
          aria-invalid={problems.description !== ""}
          aria-describedby="memory-description-note"
          onChange={(e) => {
            set({ description: e.target.value.replace(/[\r\n]+/g, " ") });
          }}
        />
        <p
          id="memory-description-note"
          className={cn(
            "text-xs",
            problems.description === "" ? "text-muted-foreground" : "text-destructive",
          )}
        >
          {problems.description === ""
            ? `${String(Array.from(draft.description).length)} / 200 characters`
            : problems.description}
        </p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="memory-type">Type</Label>
          <Select
            value={draft.type}
            onValueChange={(v) => {
              set({ type: v as MemoryType });
            }}
          >
            <SelectTrigger id="memory-type" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {memoryTypes.map((t) => (
                <SelectItem key={t} value={t}>
                  {typeLabel[t]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="memory-expires">Expires</Label>
          <div className="flex gap-2">
            <Input
              id="memory-expires"
              type="date"
              value={draft.expires}
              onChange={(e) => {
                set({ expires: e.target.value });
              }}
            />
            <Button
              type="button"
              variant="outline"
              disabled={draft.expires === ""}
              onClick={() => {
                set({ expires: "" });
              }}
            >
              No expiry
            </Button>
          </div>
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="memory-body">Body</Label>
        <div className="focus-within:border-ring focus-within:ring-ring/50 rounded-md border focus-within:ring-3">
          <MarkdownField
            id="memory-body"
            value={draft.body}
            mode="markdown"
            touch={touch}
            rows={8}
            // The touch rule's min-height outranks a class, so the height is inline.
            style={{ minHeight: "10rem" }}
            aria-invalid={problems.body !== ""}
            aria-describedby="memory-body-note"
            className="max-h-96 resize-none border-0 bg-transparent focus-visible:border-0 focus-visible:ring-0 dark:bg-transparent"
            onSend={save}
            onChange={(e) => {
              set({ body: e.target.value });
            }}
          />
        </div>
        <p
          id="memory-body-note"
          className={cn(
            "text-xs",
            problems.body === "" ? "text-muted-foreground" : "text-destructive",
          )}
        >
          {problems.body === ""
            ? `${String(byteLength(draft.body))} / ${String(bodyLimit)} bytes. Markdown.`
            : problems.body}
        </p>
      </div>
      <div className="flex flex-wrap items-center justify-end gap-2 border-t pt-3">
        <Button
          type="button"
          variant="ghost"
          disabled={changes === null || busy}
          onClick={() => {
            setDraft(draftOf(base));
            setOutcome(null);
          }}
        >
          Discard changes
        </Button>
        <Button type="submit" disabled={changes === null || invalid || busy}>
          {edit.isPending ? "Saving…" : "Save memory"}
        </Button>
      </div>
      <div
        role="group"
        aria-label="Memory actions"
        className="flex flex-wrap items-center gap-2 border-t pt-3"
      >
        <Button
          type="button"
          variant="outline"
          disabled={busy || (!base.pinned && base.state !== "active")}
          title={
            !base.pinned && base.state !== "active"
              ? "Only an active memory can be pinned."
              : undefined
          }
          onClick={() => {
            setPinned(!base.pinned);
          }}
        >
          {base.pinned ? "Unpin" : "Pin"}
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={busy || base.state === "archived"}
          onClick={archiveIt}
        >
          Archive
        </Button>
        <Button
          type="button"
          variant="destructive"
          className="sm:ml-auto"
          disabled={busy}
          onClick={() => {
            setPurging(true);
          }}
        >
          Purge
        </Button>
      </div>
      <ConfirmDialog
        open={purging}
        onOpenChange={setPurging}
        title={`Purge ${memory.name}?`}
        description="This removes the memory's file and its index entry for good. It cannot be undone here. Earlier versions in the memory repository's git history, old conversations that quoted it and backups may still hold the text. To forget it and keep it recoverable, archive it instead."
        confirmLabel="Purge memory"
        onConfirm={purgeIt}
      />
    </form>
  );
}

/** Result says what the last action did, in the tone of how it went. */
function Result({ outcome, state }: { outcome: Outcome | null; state: Memory["state"] }) {
  if (outcome === null) return null;
  switch (outcome.kind) {
    case "saved":
      return (
        <Notice tone="success">
          Saved.{state === "pending" ? " It is still waiting for review." : ""}
          {state === "archived" ? " It is still archived." : ""}
        </Notice>
      );
    case "pending":
      return (
        <Notice tone="warning">
          Saved, but the text was flagged. The memory is now pending and unpinned: it is not used
          until you accept it.
        </Notice>
      );
    case "pinned":
      return <Notice tone="success">Pinned. It is in the preamble at the next compaction.</Notice>;
    case "unpinned":
      return <Notice tone="success">Unpinned.</Notice>;
    case "archived":
      return <Notice tone="success">Archived. It is no longer used.</Notice>;
    case "failed":
      if (isUnavailable(outcome.error)) {
        return (
          <Notice tone="warning">
            Memory cannot do that right now, and nothing was changed.{" "}
            {outcome.error instanceof Error ? `${outcome.error.message}. ` : ""}Try again in a
            moment.
          </Notice>
        );
      }
      return <ActionError action={outcome.action} error={outcome.error} />;
  }
}

/** Conflict shows the version that won over the draft and lets the user pick one. */
function Conflict({
  current,
  onReapply,
  onUseCurrent,
}: {
  current: Memory;
  onReapply: () => void;
  onUseCurrent: () => void;
}) {
  return (
    <Notice tone="warning" className="flex-col">
      <div className="space-y-2" role="group" aria-label="Changed memory">
        <p className="font-medium">This memory changed since you opened it. Nothing was saved.</p>
        <dl className="bg-background/60 text-foreground grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 rounded-md border p-2">
          <dt className="text-muted-foreground">Description</dt>
          <dd className="break-words">{current.description}</dd>
          <dt className="text-muted-foreground">Type</dt>
          <dd>{typeLabel[current.type]}</dd>
          <dt className="text-muted-foreground">Expires</dt>
          <dd>{current.expires === "" ? "No expiry" : current.expires}</dd>
          <dt className="text-muted-foreground">Body</dt>
          <dd className="font-mono break-words whitespace-pre-wrap">{current.body}</dd>
        </dl>
        <div className="flex flex-wrap gap-2">
          <Button type="button" size="sm" onClick={onReapply}>
            Keep my edits and save again
          </Button>
          <Button type="button" size="sm" variant="outline" onClick={onUseCurrent}>
            Use the current version
          </Button>
        </div>
        <p>Keeping your edits replaces the fields above with yours when you press Save memory.</p>
      </div>
    </Notice>
  );
}
