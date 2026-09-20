/**
 * What the Files panel is editing, per workspace: the open file, the unsaved
 * text typed into it, which directories are expanded, and a file waiting on
 * "discard your changes?". It is a store rather than component state so that
 * switching panel tabs keeps an unsaved edit. The rules are plain functions,
 * tested alone.
 */

import { create } from "zustand";

/** OpenFile is the file in the editor and what was typed into it since it was saved. */
export type OpenFile = {
  path: string;
  /** draft is the editor's text; undefined until the user types. */
  draft?: string;
};

/** WorkspaceFiles is the panel's state for one workspace. */
export type WorkspaceFiles = {
  expanded: readonly string[];
  open?: OpenFile;
  /** pending is a file the user asked for while the open one had unsaved changes. */
  pending?: string;
};

/** isDirty says whether the open file has text the harness does not. */
export function isDirty(open: OpenFile | undefined, saved: string | undefined): boolean {
  return open?.draft !== undefined && open.draft !== saved;
}

/** OpenDecision is what asking for a file does: nothing, open it, or ask first. */
export type OpenDecision = "stay" | "open" | "confirm";

/** decideOpen decides what asking for next does while open is in the editor. */
export function decideOpen(
  open: OpenFile | undefined,
  saved: string | undefined,
  next: string,
): OpenDecision {
  if (open?.path === next) return "stay";
  return isDirty(open, saved) ? "confirm" : "open";
}

/**
 * afterSave is the open file once text was saved: the draft is dropped when
 * it is what was saved, and kept when the user typed on during the save.
 */
export function afterSave(
  open: OpenFile | undefined,
  path: string,
  text: string,
): OpenFile | undefined {
  if (open?.path !== path || open.draft !== text) return open;
  return { path };
}

/** parentDir is the directory holding path, "" for the root. */
export function parentDir(path: string): string {
  const at = path.lastIndexOf("/");
  return at < 0 ? "" : path.slice(0, at);
}

/** toggle expands a collapsed directory or collapses an expanded one. */
export function toggle(expanded: readonly string[], path: string): readonly string[] {
  return expanded.includes(path) ? expanded.filter((p) => p !== path) : [...expanded, path];
}

const empty: WorkspaceFiles = { expanded: [] };

type FilesState = {
  byWorkspace: Record<string, WorkspaceFiles>;
  update: (workspaceId: string, change: (files: WorkspaceFiles) => WorkspaceFiles) => void;
};

/** useFilesStore holds every workspace's WorkspaceFiles; read one with useWorkspaceFiles. */
export const useFilesStore = create<FilesState>((set) => ({
  byWorkspace: {},
  update: (workspaceId, change) => {
    set((state) => ({
      byWorkspace: {
        ...state.byWorkspace,
        [workspaceId]: change(state.byWorkspace[workspaceId] ?? empty),
      },
    }));
  },
}));

/** useWorkspaceFiles reads one workspace's state and an updater bound to it. */
export function useWorkspaceFiles(
  workspaceId: string,
): [WorkspaceFiles, (change: (files: WorkspaceFiles) => WorkspaceFiles) => void] {
  const files = useFilesStore((s) => s.byWorkspace[workspaceId] ?? empty);
  const update = useFilesStore((s) => s.update);
  return [
    files,
    (change) => {
      update(workspaceId, change);
    },
  ];
}
