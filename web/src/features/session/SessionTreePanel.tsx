/**
 * The session tree panel. It draws the outline as a tree and offers the two
 * things a node is good for: moving the head there, and forking the path down
 * to it into a session of its own.
 */

import { GitBranch, Locate } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";

import type { Node } from "@/api/types";
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
import { cn } from "@/lib/utils";

export type SessionTreePanelProps = {
  sessionId: string;
};

/** childrenOf groups the outline by parent so it can be walked as a tree. */
function childrenOf(nodes: Node[]): Map<string, Node[]> {
  const byParent = new Map<string, Node[]>();
  for (const node of nodes) {
    const key = node.parent_id ?? "";
    byParent.set(key, [...(byParent.get(key) ?? []), node]);
  }
  return byParent;
}

export function SessionTreePanel({ sessionId }: SessionTreePanelProps) {
  const outline = useSessionOutline(sessionId);
  const setHead = useSetSessionHead(sessionId);
  const fork = useForkSession(sessionId);
  const navigate = useNavigate();
  const [confirmHead, setConfirmHead] = useState<Node | null>(null);
  const [forkFrom, setForkFrom] = useState<Node | null>(null);
  const [forkTitle, setForkTitle] = useState("");

  if (outline.isPending) {
    return <p className="text-muted-foreground p-3 text-xs">Loading the tree…</p>;
  }
  if (outline.isError) {
    return (
      <p role="alert" className="text-destructive p-3 text-xs">
        {outline.error.message}
      </p>
    );
  }

  const byParent = childrenOf(outline.data.nodes);
  const head = outline.data.head_entry_id;

  const rows = (parent: string, depth: number): React.ReactNode =>
    (byParent.get(parent) ?? []).map((node) => (
      <li key={node.id}>
        <div
          className={cn(
            "hover:bg-accent/50 group flex items-center gap-1 rounded-sm py-0.5 pr-1",
            node.id === head && "bg-accent",
          )}
          style={{ paddingLeft: `${String(depth * 12 + 4)}px` }}
        >
          <span className="text-muted-foreground w-[5.5rem] shrink-0 truncate font-mono text-[0.7rem]">
            {node.kind}
          </span>
          <span className="min-w-0 flex-1 truncate text-xs" title={node.preview}>
            {node.preview || "—"}
          </span>
          <Button
            size="icon"
            variant="ghost"
            className="size-6 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
            aria-label={`Set head to ${node.kind} entry`}
            onClick={() => {
              setConfirmHead(node);
            }}
          >
            <Locate aria-hidden className="size-3" />
          </Button>
          <Button
            size="icon"
            variant="ghost"
            className="size-6 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
            aria-label={`Fork from ${node.kind} entry`}
            onClick={() => {
              setForkFrom(node);
              setForkTitle(node.preview.slice(0, 40) || "Fork");
            }}
          >
            <GitBranch aria-hidden className="size-3" />
          </Button>
        </div>
        <ul>{rows(node.id, depth + 1)}</ul>
      </li>
    ));

  return (
    <div className="p-2">
      <ul role="tree" aria-label="Session tree">
        {rows("", 0)}
      </ul>

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
          <p className="bg-muted rounded-md p-2 font-mono text-xs">{confirmHead?.preview}</p>
          {setHead.isError && (
            <p role="alert" className="text-destructive text-xs">
              {setHead.error.message}
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
              {fork.error.message}
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
