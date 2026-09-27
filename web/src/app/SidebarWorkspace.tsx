/** A workspace in the sidebar, with its actions and, when open, its sessions. */

import {
  Archive,
  ArchiveRestore,
  Pencil,
  Pin,
  PinOff,
  Play,
  Plus,
  Square,
  Trash2,
} from "lucide-react";
import { useMemo, useState } from "react";
import { useNavigate } from "react-router";

import type { Workspace } from "@/api/types";
import { SessionRow } from "@/app/SidebarSession";
import { ArchivedGroup, IconButton, Row, RowFailure } from "@/app/SidebarRow";
import { useRenaming } from "@/app/useRenaming";
import type { RowAction } from "@/app/SidebarRow";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { sessionTree, splitArchived, useCreateSession, useSessions } from "@/features/sessions";
import {
  useDeleteWorkspace,
  useUpdateWorkspace,
  useWorkspaceAction,
  WorkspaceStateBadge,
} from "@/features/workspaces";

/** archivedStep is how much further in the rows under an Archived heading sit. */
const archivedStep = 12;

type WorkspaceRowProps = {
  workspace: Workspace;
  /** indent is the row's left padding, in pixels. */
  indent: number;
  open: boolean;
  onToggle: () => void;
  sessionId?: string;
  onNavigate?: () => void;
};

export function WorkspaceRow({
  workspace,
  indent,
  open,
  onToggle,
  sessionId,
  onNavigate,
}: WorkspaceRowProps) {
  const action = useWorkspaceAction();
  const update = useUpdateWorkspace();
  const remove = useDeleteWorkspace();
  const create = useCreateSession();
  const navigate = useNavigate();
  const renaming = useRenaming();
  const [confirming, setConfirming] = useState(false);

  const running = workspace.state === "running";
  // A worktree runs in the container of the workspace holding it, which is
  // the one that starts and stops.
  const worktree = workspace.worktree_of !== undefined;
  const lifecycle: RowAction[] = worktree
    ? []
    : [
        {
          label: running ? "Stop" : "Start",
          icon: running ? Square : Play,
          disabled:
            action.isPending || workspace.state === "creating" || workspace.state === "gone",
          onSelect: () => {
            action.mutate({ id: workspace.id, action: running ? "stop" : "start" });
          },
        },
      ];
  const menu: RowAction[] = [
    ...lifecycle,
    {
      label: "Rename",
      icon: Pencil,
      shortcut: "F2",
      takesFocus: true,
      separated: !worktree,
      onSelect: renaming.start,
    },
    {
      label: workspace.pinned ? "Unpin" : "Pin",
      icon: workspace.pinned ? PinOff : Pin,
      onSelect: () => {
        update.mutate({ id: workspace.id, changes: { pinned: !workspace.pinned } });
      },
    },
    {
      label: workspace.archived ? "Unarchive" : "Archive",
      icon: workspace.archived ? ArchiveRestore : Archive,
      onSelect: () => {
        update.mutate({ id: workspace.id, changes: { archived: !workspace.archived } });
      },
    },
    {
      label: "Delete",
      icon: Trash2,
      destructive: true,
      separated: true,
      onSelect: () => {
        setConfirming(true);
      },
    },
  ];

  return (
    <li>
      <Row
        indent={indent}
        expanded={open}
        onClick={onToggle}
        icon={<WorkspaceStateBadge workspaceId={workspace.id} state={workspace.state} />}
        label={workspace.name}
        meta={workspace.branch}
        pinned={workspace.pinned}
        quick={
          <IconButton
            label={`New session in ${workspace.name}`}
            disabled={create.isPending}
            onClick={() => {
              create.mutate(
                { workspace_id: workspace.id },
                {
                  onSuccess: (session) => {
                    onNavigate?.();
                    void navigate(`/sessions/${session.id}`);
                  },
                },
              );
            }}
          >
            <Plus aria-hidden className="size-3" />
          </IconButton>
        }
        menu={menu}
        renaming={renaming}
        onRename={(name) => {
          update.mutate({ id: workspace.id, changes: { name } });
        }}
      />
      <RowFailure
        indent={indent}
        action={running ? "stop the workspace" : "start the workspace"}
        error={action.error}
      />
      <RowFailure indent={indent} action="change the workspace" error={update.error} />
      <RowFailure indent={indent} action="destroy the workspace" error={remove.error} />
      <RowFailure indent={indent} action="start a session" error={create.error} />
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Destroy ${workspace.name}?`}
        description={
          worktree
            ? "The worktree goes with its uncommitted changes, and so do the workspace's sessions and their entries. Its branch and commits stay in the repository it shares."
            : "The container and its volume go, and so do the workspace's sessions and their entries, and any worktrees of agents working in it. Work that was not pushed to the hub is lost."
        }
        confirmLabel="Destroy workspace"
        onConfirm={() => {
          remove.mutate(workspace.id);
        }}
      />
      {open && (
        <SessionList
          workspaceId={workspace.id}
          indent={indent + 24}
          sessionId={sessionId}
          onNavigate={onNavigate}
        />
      )}
    </li>
  );
}

type SessionListProps = {
  workspaceId: string;
  /** indent is the left padding of a session row directly under the workspace. */
  indent: number;
  sessionId?: string;
  onNavigate?: () => void;
};

/**
 * SessionList holds the session query, for the same reason WorkspaceList
 * does. It asks for the descendants as well, because a fork with a workspace
 * and a child agent both live somewhere else and still belong under the
 * session they came from. Archived sessions fold under a heading at the end.
 */
function SessionList({ workspaceId, indent, sessionId, onNavigate }: SessionListProps) {
  const sessions = useSessions(workspaceId, true);
  const { active, archived } = useMemo(
    () => splitArchived(sessionTree(sessions.data ?? [])),
    [sessions.data],
  );
  const row = (node: (typeof active)[number], at: number) => (
    <SessionRow
      key={node.session.id}
      node={node}
      depth={0}
      indent={at}
      sessionId={sessionId}
      onNavigate={onNavigate}
    />
  );
  return (
    <ul>
      {sessions.isPending && (
        <li
          className="text-muted-foreground py-1 text-xs"
          style={{ paddingLeft: `${String(indent)}px` }}
        >
          Loading…
        </li>
      )}
      {active.map((node) => row(node, indent))}
      {sessions.data !== undefined && active.length === 0 && (
        <li
          className="text-muted-foreground py-1 text-xs"
          style={{ paddingLeft: `${String(indent)}px` }}
        >
          No sessions.
        </li>
      )}
      <ArchivedGroup count={archived.length} indent={indent} what="sessions">
        {archived.map((node) => row(node, indent + archivedStep))}
      </ArchivedGroup>
    </ul>
  );
}
