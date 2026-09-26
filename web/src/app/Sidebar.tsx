/**
 * The left pane: projects, their workspaces, and their sessions, with the
 * actions that create and remove each, and below them the chats, which are
 * sessions in no workspace. It is the only place that composes the list
 * features, so it lives in `app/` rather than in one of them.
 */

import { Plus } from "lucide-react";
import { useState } from "react";

import type { Project } from "@/api/types";
import { ChatList } from "@/app/SidebarChats";
import { ProjectRow } from "@/app/SidebarProject";
import { Button } from "@/components/ui/button";
import { CreateProjectDialog, useProjects } from "@/features/projects";
import { CreateWorkspaceDialog } from "@/features/workspaces";
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
    <div className="flex h-full min-h-0 flex-col">
      <nav aria-label="Projects" className="flex min-h-0 flex-1 flex-col">
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
      </nav>
      <ChatList sessionId={sessionId} onNavigate={onNavigate} />

      <CreateProjectDialog open={creatingProject} onOpenChange={setCreatingProject} />
      <CreateWorkspaceDialog
        project={workspaceFor}
        onOpenChange={() => {
          setWorkspaceFor(null);
        }}
      />
    </div>
  );
}
