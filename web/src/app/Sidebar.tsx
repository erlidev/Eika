/**
 * The left pane: projects, their workspaces, and their sessions, with the
 * actions that create and remove each. It is the only place that composes the
 * three list features, so it lives in `app/` rather than in one of them.
 */

import {
  ChevronDown,
  ChevronRight,
  FolderGit2,
  MessageSquare,
  Play,
  Plus,
  Settings2,
  Square,
  Trash2,
} from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";

import type { Project, Session, Workspace } from "@/api/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Button } from "@/components/ui/button";
import {
  CreateProjectDialog,
  ProjectSettingsDialog,
  useDeleteProject,
  useProjects,
} from "@/features/projects";
import { useCreateSession, useDeleteSession, useSessions } from "@/features/sessions";
import {
  CreateWorkspaceDialog,
  useDeleteWorkspace,
  useWorkspaceAction,
  useWorkspaces,
  WorkspaceStateBadge,
} from "@/features/workspaces";
import { cn } from "@/lib/utils";
import { failureText } from "@/lib/failure";

export type SidebarProps = {
  /** sessionId is the open session, highlighted in the tree. */
  sessionId?: string;
  /**
   * onNarrowNavigate is called when the tree opens a session. The narrow
   * layout uses it to close the drawer the tree was drawn in.
   */
  onNavigate?: () => void;
};

export function Sidebar({ sessionId, onNavigate }: SidebarProps) {
  const projects = useProjects();
  const [creatingProject, setCreatingProject] = useState(false);
  const [workspaceFor, setWorkspaceFor] = useState<Project | null>(null);
  const [expanded, setExpanded] = useState<readonly string[]>([]);

  const toggle = (id: string) => {
    setExpanded((open) => (open.includes(id) ? open.filter((x) => x !== id) : [...open, id]));
  };

  return (
    <nav aria-label="Projects" className="flex h-full min-h-0 flex-col">
      <header className="flex items-center gap-1 border-b px-2 py-1.5">
        <h2 className="flex-1 text-xs font-semibold tracking-wide uppercase">Projects</h2>
        <Button
          size="icon"
          variant="ghost"
          className="size-6"
          aria-label="New project"
          onClick={() => {
            setCreatingProject(true);
          }}
        >
          <Plus aria-hidden className="size-3.5" />
        </Button>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto p-1">
        {projects.isPending && <p className="text-muted-foreground p-2 text-xs">Loading…</p>}
        {projects.isError && (
          <p role="alert" className="text-destructive p-2 text-xs">
            {failureText("load the projects", projects.error)}
          </p>
        )}
        {projects.data?.length === 0 && (
          <p className="text-muted-foreground p-2 text-xs">
            No projects yet. Add one to create a workspace.
          </p>
        )}
        <ul>
          {(projects.data ?? []).map((project) => (
            <ProjectRow
              key={project.id}
              project={project}
              open={expanded.includes(project.id)}
              onToggle={() => {
                toggle(project.id);
              }}
              onAddWorkspace={() => {
                setWorkspaceFor(project);
              }}
              openIds={expanded}
              onToggleId={toggle}
              sessionId={sessionId}
              onNavigate={onNavigate}
            />
          ))}
        </ul>
      </div>

      <CreateProjectDialog open={creatingProject} onOpenChange={setCreatingProject} />
      <CreateWorkspaceDialog
        project={workspaceFor}
        onOpenChange={() => {
          setWorkspaceFor(null);
        }}
      />
    </nav>
  );
}

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

function ProjectRow({
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
      {(workspaces.data ?? []).map((workspace) => (
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
      {workspaces.data?.length === 0 && (
        <li className="text-muted-foreground py-1 pl-8 text-xs">No workspaces.</li>
      )}
    </ul>
  );
}

type WorkspaceRowProps = {
  workspace: Workspace;
  open: boolean;
  onToggle: () => void;
  sessionId?: string;
  onNavigate?: () => void;
};

function WorkspaceRow({ workspace, open, onToggle, sessionId, onNavigate }: WorkspaceRowProps) {
  const action = useWorkspaceAction();
  const remove = useDeleteWorkspace();
  const create = useCreateSession();
  const navigate = useNavigate();
  const [confirming, setConfirming] = useState(false);

  const running = workspace.state === "running";

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
            <IconButton
              label={`New session in ${workspace.name}`}
              onClick={() => {
                create.mutate(
                  { workspace_id: workspace.id, title: "New session" },
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
        description="The container and its volume go, and so do the workspace's sessions and their entries. Work that was not pushed to the hub is lost."
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

/** SessionList holds the session query, for the same reason WorkspaceList does. */
function SessionList({
  workspaceId,
  sessionId,
  onNavigate,
}: {
  workspaceId: string;
  sessionId?: string;
  onNavigate?: () => void;
}) {
  const sessions = useSessions(workspaceId);
  return (
    <ul>
      {sessions.isPending && <li className="text-muted-foreground py-1 pl-12 text-xs">Loading…</li>}
      {(sessions.data ?? []).map((session) => (
        <SessionRow
          key={session.id}
          session={session}
          current={session.id === sessionId}
          onNavigate={onNavigate}
        />
      ))}
      {sessions.data?.length === 0 && (
        <li className="text-muted-foreground py-1 pl-12 text-xs">No sessions.</li>
      )}
    </ul>
  );
}

function SessionRow({
  session,
  current,
  onNavigate,
}: {
  session: Session;
  current: boolean;
  onNavigate?: () => void;
}) {
  const remove = useDeleteSession();
  const navigate = useNavigate();
  const [confirming, setConfirming] = useState(false);
  return (
    <li>
      <div
        className={cn(
          "group hover:bg-accent/50 flex items-center gap-1 rounded-md pr-1",
          current && "bg-accent",
        )}
      >
        <button
          type="button"
          aria-current={current ? "page" : undefined}
          className="focus-visible:ring-ring flex min-w-0 flex-1 items-center gap-1.5 py-1 pl-10 text-left text-xs focus-visible:ring-1 focus-visible:outline-none"
          onClick={() => {
            onNavigate?.();
            void navigate(`/sessions/${session.id}`);
          }}
        >
          <MessageSquare aria-hidden className="size-3.5 shrink-0" />
          <span className="truncate">{session.title}</span>
        </button>
        <IconButton
          label={`Delete ${session.title}`}
          destructive
          onClick={() => {
            setConfirming(true);
          }}
        >
          <Trash2 aria-hidden className="size-3" />
        </IconButton>
      </div>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Delete ${session.title}?`}
        description="The session's run is aborted and its entries are deleted. The workspace and its files stay."
        confirmLabel="Delete session"
        onConfirm={() => {
          remove.mutate(session.id);
        }}
      />
    </li>
  );
}

type RowProps = {
  depth: number;
  open: boolean;
  onToggle: () => void;
  icon: React.ReactNode;
  label: string;
  meta?: string;
  actions: React.ReactNode;
};

function Row({ depth, open, onToggle, icon, label, meta, actions }: RowProps) {
  return (
    <div className="group hover:bg-accent/50 flex items-center gap-1 rounded-md pr-1">
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        style={{ paddingLeft: `${String(depth * 12 + 4)}px` }}
        className="focus-visible:ring-ring flex min-w-0 flex-1 items-center gap-1.5 py-1 text-left text-xs focus-visible:ring-1 focus-visible:outline-none"
      >
        {open ? (
          <ChevronDown aria-hidden className="size-3.5 shrink-0" />
        ) : (
          <ChevronRight aria-hidden className="size-3.5 shrink-0" />
        )}
        {icon}
        <span className="truncate font-medium">{label}</span>
        {meta !== undefined && meta !== "" && (
          <span className="text-muted-foreground shrink-0 truncate font-mono text-2xs">{meta}</span>
        )}
      </button>
      <span className="flex shrink-0 gap-0.5 opacity-0 group-focus-within:opacity-100 group-hover:opacity-100">
        {actions}
      </span>
    </div>
  );
}

function IconButton({
  label,
  children,
  onClick,
  disabled,
  destructive,
}: {
  label: string;
  children: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  destructive?: boolean;
}) {
  return (
    <Button
      size="icon-xs"
      variant="ghost"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
      className={cn(destructive && "hover:text-destructive")}
    >
      {children}
    </Button>
  );
}
