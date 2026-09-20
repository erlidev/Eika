/**
 * The workspace's files as a tree that lists a directory only when it is
 * expanded. Rows are flat siblings with `aria-level`, as the WAI-ARIA tree
 * pattern allows, so the arrows move through them in document order: Up and
 * Down move, Right expands or steps in, Left collapses or steps out, Home and
 * End jump, and Enter opens.
 */

import { ChevronDown, ChevronRight, File, Folder, FolderOpen } from "lucide-react";
import { useRef, useState } from "react";

import type { FileEntry } from "@/api/types";
import { LoadError } from "@/components/Notice";
import { parentDir } from "@/features/files/editing";
import { useDirectory } from "@/features/files/queries";
import { failureText } from "@/lib/failure";
import { cn } from "@/lib/utils";

export type FileTreeProps = {
  workspaceId: string;
  expanded: readonly string[];
  /** selected is the file open in the editor. */
  selected?: string;
  onToggle: (path: string) => void;
  onOpen: (path: string) => void;
};

export function FileTree({ workspaceId, expanded, selected, onToggle, onOpen }: FileTreeProps) {
  const tree = useRef<HTMLDivElement>(null);
  // The tree is one tab stop: the row last focused takes it, or the first row.
  const [focusPath, setFocusPath] = useState<string | undefined>(undefined);

  const rows = () => [...(tree.current?.querySelectorAll<HTMLElement>('[role="treeitem"]') ?? [])];

  const focusRow = (row: HTMLElement | undefined) => {
    row?.focus();
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const all = rows();
    const at = all.findIndex((row) => row === document.activeElement);
    const row = all[at];
    if (!row) return;
    const path = row.dataset.path ?? "";
    const isDir = row.dataset.dir === "true";
    const open = row.getAttribute("aria-expanded") === "true";
    switch (e.key) {
      case "ArrowDown":
        focusRow(all[Math.min(at + 1, all.length - 1)]);
        break;
      case "ArrowUp":
        focusRow(all[Math.max(at - 1, 0)]);
        break;
      case "Home":
        focusRow(all[0]);
        break;
      case "End":
        focusRow(all.at(-1));
        break;
      case "ArrowRight":
        if (isDir && !open) onToggle(path);
        else if (isDir) focusRow(all[at + 1]);
        break;
      case "ArrowLeft":
        if (isDir && open) onToggle(path);
        else
          focusRow(all.find((r) => r.dataset.path === parentDir(path) && parentDir(path) !== ""));
        break;
      case "Enter":
      case " ":
        if (isDir) onToggle(path);
        else onOpen(path);
        break;
      default:
        return;
    }
    e.preventDefault();
  };

  return (
    <div
      ref={tree}
      role="tree"
      aria-label="Files"
      className="py-1 font-mono text-xs"
      onKeyDown={onKeyDown}
      onFocus={(e) => {
        const path = e.target.dataset.path;
        if (path !== undefined) setFocusPath(path);
      }}
    >
      <DirectoryRows
        workspaceId={workspaceId}
        path=""
        level={1}
        expanded={expanded}
        selected={selected}
        focusPath={focusPath}
        onToggle={onToggle}
        onOpen={onOpen}
      />
    </div>
  );
}

type DirectoryRowsProps = {
  workspaceId: string;
  path: string;
  level: number;
  expanded: readonly string[];
  selected: string | undefined;
  focusPath: string | undefined;
  onToggle: (path: string) => void;
  onOpen: (path: string) => void;
};

/** indent is the left padding of a row at a level: one step per level. */
function indent(level: number): React.CSSProperties {
  return { paddingLeft: `${String(0.5 + (level - 1) * 0.75)}rem` };
}

function DirectoryRows(props: DirectoryRowsProps) {
  const { workspaceId, path, level, expanded, selected, focusPath, onToggle, onOpen } = props;
  const listing = useDirectory(workspaceId, path);
  // git's own directory is not the user's to edit.
  const entries = listing.data?.filter((entry) => !(entry.is_dir && entry.name === ".git")) ?? [];

  if (listing.isPending) {
    return (
      <div className="text-muted-foreground py-0.5 font-sans" style={indent(level)}>
        Loading…
      </div>
    );
  }
  if (listing.isError) {
    if (level === 1) {
      return (
        <div className="px-2">
          <LoadError
            what="the files"
            error={listing.error}
            retrying={listing.isFetching}
            retry={() => void listing.refetch()}
          />
        </div>
      );
    }
    return (
      <div role="alert" className="text-destructive py-0.5 pr-2 font-sans" style={indent(level)}>
        {failureText(`list ${path}`, listing.error)}
      </div>
    );
  }
  if (entries.length === 0) {
    return (
      <div className="text-muted-foreground py-0.5 font-sans" style={indent(level)}>
        {level === 1 ? "The workspace is empty." : "Empty"}
      </div>
    );
  }
  return entries.map((entry, index) => {
    const open = entry.is_dir && expanded.includes(entry.path);
    const first = level === 1 && index === 0;
    const tabbable = focusPath === undefined ? first : focusPath === entry.path;
    return (
      <div key={entry.path} className="contents">
        <Row
          entry={entry}
          level={level}
          open={open}
          selected={entry.path === selected}
          tabbable={tabbable}
          position={index + 1}
          count={entries.length}
          onActivate={() => {
            if (entry.is_dir) onToggle(entry.path);
            else onOpen(entry.path);
          }}
        />
        {open && <DirectoryRows {...props} path={entry.path} level={level + 1} />}
      </div>
    );
  });
}

type RowProps = {
  entry: FileEntry;
  level: number;
  open: boolean;
  selected: boolean;
  tabbable: boolean;
  position: number;
  count: number;
  onActivate: () => void;
};

function Row({ entry, level, open, selected, tabbable, position, count, onActivate }: RowProps) {
  const Chevron = open ? ChevronDown : ChevronRight;
  const Icon = entry.is_dir ? (open ? FolderOpen : Folder) : File;
  return (
    <div
      role="treeitem"
      aria-level={level}
      aria-posinset={position}
      aria-setsize={count}
      aria-expanded={entry.is_dir ? open : undefined}
      aria-selected={selected}
      data-path={entry.path}
      data-dir={entry.is_dir}
      tabIndex={tabbable ? 0 : -1}
      title={entry.path}
      className={cn(
        "hover:bg-accent focus-visible:ring-ring flex items-center gap-1 py-0.5 pr-2 transition-colors select-none focus-visible:ring-1 focus-visible:outline-none focus-visible:ring-inset",
        selected && "bg-accent",
      )}
      style={indent(level)}
      onClick={onActivate}
    >
      {entry.is_dir ? (
        <Chevron aria-hidden className="text-muted-foreground size-3.5 shrink-0" />
      ) : (
        <span aria-hidden className="size-3.5 shrink-0" />
      )}
      <Icon aria-hidden className="text-muted-foreground size-3.5 shrink-0" />
      <span className="truncate">{entry.name}</span>
    </div>
  );
}
