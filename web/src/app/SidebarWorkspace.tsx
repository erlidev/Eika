/** A workspace in the sidebar, with its actions and, when open, its sessions. */

import { Play, Plus, Square, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { useNavigate } from "react-router";

import type { Workspace } from "@/api/types";
import { IconButton, Row } from "@/app/SidebarRow";
import { SessionRow } from "@/app/SidebarSession";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { sessionTree, useCreateSession, useSessions } from "@/features/sessions";
import { useDeleteWorkspace, useWorkspaceAction, WorkspaceStateBadge } from "@/features/workspaces";

type WorkspaceRowProps = {
  workspace: Workspace;
  open: boolean;
  onToggle: () => void;
  sessionId?: string;
  onNavigate?: () => void;
};

export function WorkspaceRow({
  workspace,
  open,
  onToggle,
  sessionId,
  onNavigate,
}: WorkspaceRowProps) {
  const action = useWorkspaceAction();
  const remove = useDeleteWorkspace();
  const create = useCreateSession();
  const navigate = useNavigate();
  const [confirming, setConfirming] = useState(false);

  const running = workspace.state === "running";
  // A worktree runs in the container of the workspace holding it, which is
  // the one that starts and stops.
  const worktree = workspace.worktree_of !== undefined;

  return (
    <li>
      <Row
        depth={1}
        open={open}
        onToggle={onToggle}
        icon={<WorkspaceStateBadge workspaceId={workspace.id} state={workspace.state} />}
        label={workspace.name}
        meta={workspace.branch}
        actions={
          <>
            {!worktree && (
              <IconButton
                label={running ? `Stop ${workspace.name}` : `Start ${workspace.name}`}
                disabled={action.isPending}
                onClick={() => {
                  action.mutate({ id: workspace.id, action: running ? "stop" : "start" });
                }}
              >
                {running ? (
                  <Square aria-hidden className="size-3" />
                ) : (
                  <Play aria-hidden className="size-3" />
                )}
              </IconButton>
            )}
            <IconButton
              label={`New session in ${workspace.name}`}
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
            <IconButton
              label={`Delete ${workspace.name}`}
              destructive
              onClick={() => {
                setConfirming(true);
              }}
            >
              <Trash2 aria-hidden className="size-3" />
            </IconButton>
          </>
        }
      />
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
        <SessionList workspaceId={workspace.id} sessionId={sessionId} onNavigate={onNavigate} />
      )}
    </li>
  );
}

type SessionListProps = {
  workspaceId: string;
  sessionId?: string;
  onNavigate?: () => void;
};

/**
 * SessionList holds the session query, for the same reason WorkspaceList
 * does. It asks for the descendants as well, because a fork with a workspace
 * and a child agent both live somewhere else and still belong under the
 * session they came from.
 */
function SessionList({ workspaceId, sessionId, onNavigate }: SessionListProps) {
  const sessions = useSessions(workspaceId, true);
  const tree = useMemo(() => sessionTree(sessions.data ?? []), [sessions.data]);
  return (
    <ul>
      {sessions.isPending && <li className="text-muted-foreground py-1 pl-12 text-xs">Loading…</li>}
      {tree.map((node) => (
        <SessionRow
          key={node.session.id}
          node={node}
          depth={0}
          indent={40}
          sessionId={sessionId}
          onNavigate={onNavigate}
        />
      ))}
      {sessions.data?.length === 0 && (
        <li className="text-muted-foreground py-1 pl-12 text-xs">No sessions.</li>
      )}
    </ul>
  );
}
