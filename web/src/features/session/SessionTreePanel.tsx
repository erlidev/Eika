/**
 * The session tree panel. It draws the outline as a tree and offers the two
 * things a node is good for: moving the head there, and forking the path down
 * to it into a session of its own.
 */

import { GitBranch, Locate } from "lucide-react";
import { useRef, useState } from "react";
import { useNavigate } from "react-router";

import type { Node } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useForkSession, useSessionOutline, useSetSessionHead } from "@/features/session/queries";
import { nextTreeIndex, treeRows } from "@/features/session/tree";
import { cn } from "@/lib/utils";
import { failureText } from "@/lib/failure";

export type SessionTreePanelProps = {
  sessionId: string;
};

export function SessionTreePanel({ sessionId }: SessionTreePanelProps) {
  const outline = useSessionOutline(sessionId);
  const setHead = useSetSessionHead(sessionId);
  const fork = useForkSession(sessionId);
  const navigate = useNavigate();
  const [confirmHead, setConfirmHead] = useState<Node | null>(null);
  const [forkFrom, setForkFrom] = useState<Node | null>(null);
  const [forkTitle, setForkTitle] = useState("");
  // The tree is one tab stop: the row last focused, or the head, takes it.
  const [focusId, setFocusId] = useState<string | null>(null);
  const items = useRef(new Map<string, HTMLDivElement>());

  if (outline.isPending) {
    return (
      <div className="p-3">
        <Notice tone="pending">Loading the tree…</Notice>
      </div>
    );
  }
  if (outline.isError) {
    return (
      <div className="p-3">
        <LoadError
          what="the session tree"
          error={outline.error}
          retrying={outline.isFetching}
          retry={() => void outline.refetch()}
        />
      </div>
    );
  }

  const head = outline.data.head_entry_id;
  const rows = treeRows(outline.data.nodes, head);
  const focused = Math.max(
    0,
    rows.findIndex((r) => r.node.id === (focusId ?? head)),
  );

  const startFork = (node: Node) => {
    setForkFrom(node);
    setForkTitle(node.preview.slice(0, 40) || "Fork");
  };

  const onKeyDown = (e: React.KeyboardEvent, index: number, node: Node) => {
    // Keys pressed on a row's buttons are the buttons' own.
    if (e.target !== e.currentTarget) return;
    if (e.key === "Enter") {
      e.preventDefault();
      if (node.id !== head) setConfirmHead(node);
      return;
    }
    const next = nextTreeIndex(rows, index, e.key);
    if (next === null) return;
    e.preventDefault();
    const id = rows[next]?.node.id;
    if (id === undefined) return;
    setFocusId(id);
    items.current.get(id)?.focus();
  };

  return (
    <div className="p-2">
      {/* The WAI-ARIA tree pattern with flat rows: each row carries its level,
          position, and set size. Nothing collapses, so no row has
          aria-expanded. The head is the current entry. A row's buttons are
          inside it and reachable with Tab from the focused row. */}
      <div role="tree" aria-label="Session tree" aria-describedby={`${sessionId}-tree-keys`}>
        {rows.map((row, index) => {
          const { node } = row;
          const isFocused = index === focused;
          const isHead = node.id === head;
          return (
            <div
              key={node.id}
              ref={(el) => {
                if (el) items.current.set(node.id, el);
                else items.current.delete(node.id);
              }}
              role="treeitem"
              aria-level={row.level}
              aria-posinset={row.position}
              aria-setsize={row.setSize}
              aria-current={isHead ? "true" : undefined}
              aria-label={`${node.kind}: ${node.preview || "no text"}${isHead ? " (head)" : ""}`}
              tabIndex={isFocused ? 0 : -1}
              onFocus={(e) => {
                if (e.target === e.currentTarget) setFocusId(node.id);
              }}
              onKeyDown={(e) => {
                onKeyDown(e, index, node);
              }}
              className={cn(
                "hover:bg-accent group focus-visible:ring-ring flex items-center gap-1 rounded-md py-0.5 pr-1 transition-colors outline-none focus-visible:ring-2",
                isHead && "bg-accent",
              )}
              style={{ paddingLeft: `${String((row.level - 1) * 12 + 4)}px` }}
            >
              <span className="text-muted-foreground w-[5.5rem] shrink-0 truncate font-mono text-2xs">
                {node.kind}
              </span>
              <span className="min-w-0 flex-1 truncate text-xs" title={node.preview}>
                {node.preview || "—"}
              </span>
              <Button
                size="icon-xs"
                variant="ghost"
                tabIndex={isFocused ? 0 : -1}
                className="opacity-0 group-focus-within:opacity-100 group-hover:opacity-100 focus-visible:opacity-100"
                aria-label={`Set head to ${node.kind} entry`}
                title="Move the head here"
                onClick={() => {
                  setConfirmHead(node);
                }}
              >
                <Locate aria-hidden className="size-3" />
              </Button>
              <Button
                size="icon-xs"
                variant="ghost"
                tabIndex={isFocused ? 0 : -1}
                className="opacity-0 group-focus-within:opacity-100 group-hover:opacity-100 focus-visible:opacity-100"
                aria-label={`Fork from ${node.kind} entry`}
                title="Fork a new session from here"
                onClick={() => {
                  startFork(node);
                }}
              >
                <GitBranch aria-hidden className="size-3" />
              </Button>
            </div>
          );
        })}
      </div>
      <p id={`${sessionId}-tree-keys`} className="sr-only">
        Arrow keys move between entries, Enter moves the head to the focused entry, and Tab reaches
        its buttons.
      </p>

      <Dialog
        open={confirmHead !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmHead(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Move the head here?</DialogTitle>
            <DialogDescription>
              The next run continues from this entry, and the tree branches in place. Entries after
              it stay in the session but leave the conversation.
            </DialogDescription>
          </DialogHeader>
          <p className="bg-muted rounded-md p-2 text-xs">{confirmHead?.preview}</p>
          {setHead.isError && (
            <p role="alert" className="text-destructive text-xs">
              {failureText("move the head", setHead.error)}
            </p>
          )}
          <DialogFooter>
            <Button
              variant="secondary"
              onClick={() => {
                setConfirmHead(null);
              }}
            >
              Cancel
            </Button>
            <Button
              disabled={setHead.isPending}
              onClick={() => {
                const node = confirmHead;
                if (!node) return;
                setHead.mutate(node.id, {
                  onSuccess: () => {
                    setConfirmHead(null);
                  },
                });
              }}
            >
              Move head
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={forkFrom !== null}
        onOpenChange={(open) => {
          if (!open) setForkFrom(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Fork from this entry</DialogTitle>
            <DialogDescription>
              The path from the root to this entry is copied into a new session in the same
              workspace. The two sessions share no entries.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-1">
            <Label htmlFor="fork-title">Title</Label>
            <Input
              id="fork-title"
              value={forkTitle}
              onChange={(e) => {
                setForkTitle(e.target.value);
              }}
              autoComplete="off"
            />
          </div>
          {fork.isError && (
            <p role="alert" className="text-destructive text-xs">
              {failureText("fork the session", fork.error)}
            </p>
          )}
          <DialogFooter>
            <Button
              variant="secondary"
              onClick={() => {
                setForkFrom(null);
              }}
            >
              Cancel
            </Button>
            <Button
              disabled={fork.isPending || forkTitle.trim() === ""}
              onClick={() => {
                const node = forkFrom;
                if (!node) return;
                fork.mutate(
                  { entryId: node.id, title: forkTitle.trim() },
                  {
                    onSuccess: (session) => {
                      setForkFrom(null);
                      void navigate(`/sessions/${session.id}`);
                    },
                  },
                );
              }}
            >
              Fork
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
