/** A project in the sidebar, with its actions and, when open, its workspaces. */

import { FolderGit2, Plus, Settings2, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";

import type { Project } from "@/api/types";
import { IconButton, Row } from "@/app/SidebarRow";
import { WorkspaceRow } from "@/app/SidebarWorkspace";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ProjectSettingsDialog, useDeleteProject } from "@/features/projects";
import { agentWorkspaces, useSessions } from "@/features/sessions";
import { useWorkspaces } from "@/features/workspaces";
import { failureText } from "@/lib/failure";

type ProjectRowProps = {
  project: Project;
  open: boolean;
  onToggle: () => void;
  onAddWorkspace: () => void;
  openIds: readonly string[];
  onToggleId: (id: string) => void;
  sessionId?: string;
  onNavigate?: () => void;
};

export function ProjectRow({
  project,
  open,
  onToggle,
  onAddWorkspace,
  openIds,
  onToggleId,
  sessionId,
  onNavigate,
}: ProjectRowProps) {
  const remove = useDeleteProject();
  const [confirming, setConfirming] = useState(false);
  const [editing, setEditing] = useState(false);

  return (
    <li>
      <Row
        depth={0}
        open={open}
        onToggle={onToggle}
        icon={<FolderGit2 aria-hidden className="size-3.5 shrink-0" />}
        label={project.name}
        meta={project.kind}
        actions={
          <>
            <IconButton label={`New workspace in ${project.name}`} onClick={onAddWorkspace}>
              <Plus aria-hidden className="size-3" />
            </IconButton>
            <IconButton
              label={`Settings of ${project.name}`}
              onClick={() => {
                setEditing(true);
              }}
            >
              <Settings2 aria-hidden className="size-3" />
            </IconButton>
            <IconButton
              label={`Delete ${project.name}`}
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
        title={`Delete ${project.name}?`}
        description="Every workspace of this project is destroyed with its container, volume, sessions, and entries. The hub repository and the branches it holds stay."
        confirmLabel="Delete project"
        onConfirm={() => {
          remove.mutate(project.id);
        }}
      />
      <ProjectSettingsDialog project={editing ? project : null} onOpenChange={setEditing} />
      {open && (
        <WorkspaceList
          projectId={project.id}
          openIds={openIds}
          onToggleId={onToggleId}
          sessionId={sessionId}
          onNavigate={onNavigate}
        />
      )}
    </li>
  );
}

type WorkspaceListProps = {
  projectId: string;
  openIds: readonly string[];
  onToggleId: (id: string) => void;
  sessionId?: string;
  onNavigate?: () => void;
};

/**
 * WorkspaceList is a component of its own so that a collapsed project asks
 * the harness for nothing: the query lives where the rows are rendered.
 */
function WorkspaceList({
  projectId,
  openIds,
  onToggleId,
  sessionId,
  onNavigate,
}: WorkspaceListProps) {
  const workspaces = useWorkspaces(projectId);
  // Every session of the project, to find the workspaces that exist only to
  // hold a fork or a child agent. Those are reached through the session that
  // owns them and are not listed here as containers of their own.
  const sessions = useSessions();
  const owned = useMemo(() => agentWorkspaces(sessions.data ?? []), [sessions.data]);
  const listed = (workspaces.data ?? []).filter((w) => !owned.has(w.id));
  return (
    <ul>
      {workspaces.isPending && (
        <li className="text-muted-foreground py-1 pl-8 text-xs">Loading…</li>
      )}
      {workspaces.isError && (
        <li role="alert" className="text-destructive py-1 pl-8 text-xs">
          {failureText("load the workspaces", workspaces.error)}
        </li>
      )}
      {listed.map((workspace) => (
        <WorkspaceRow
          key={workspace.id}
          workspace={workspace}
          open={openIds.includes(workspace.id)}
          onToggle={() => {
            onToggleId(workspace.id);
          }}
          sessionId={sessionId}
          onNavigate={onNavigate}
        />
      ))}
      {workspaces.data !== undefined && listed.length === 0 && (
        <li className="text-muted-foreground py-1 pl-8 text-xs">No workspaces.</li>
      )}
    </ul>
  );
}
