/**
 * The board: the subagents in groups by state, each a row that opens its
 * view. A project filter appears when the subagents belong to more than one.
 */

import { NavLink } from "react-router";

import type { Project, Session } from "@/api/types";
import { LoadError, Notice } from "@/components/Notice";
import { StateDot } from "@/features/activity/StateDot";
import { useProjects, useSubagents } from "@/features/activity/queries";
import { boardGroups, liveLine, projectsOf, stateLabel } from "@/features/activity/state";
import { formatAgo } from "@/lib/format";
import { cn } from "@/lib/utils";

export type AgentBoardProps = {
  /** project narrows the board to one project's subagents; empty shows all. */
  project: string;
  onProjectChange: (project: string) => void;
};

export function AgentBoard({ project, onProjectChange }: AgentBoardProps) {
  const agents = useSubagents();
  const projects = useProjects();
  if (agents.isPending) {
    return (
      <div className="p-3">
        <Notice tone="pending">Loading the subagents…</Notice>
      </div>
    );
  }
  if (agents.isError) {
    return (
      <div className="p-3">
        <LoadError
          what="the subagents"
          error={agents.error}
          retry={() => void agents.refetch()}
          retrying={agents.isFetching}
        />
      </div>
    );
  }
  if (agents.data.length === 0) {
    return (
      <p className="text-muted-foreground p-4 text-sm">
        No subagents yet. When Eika hands work to one, it shows here.
      </p>
    );
  }
  const names = new Map((projects.data ?? []).map((p) => [p.id, p.name]));
  const filter = projectsOf(agents.data, projects.data ?? []);
  const groups = boardGroups(agents.data, project);
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto">
      {filter.length > 1 && (
        <ProjectFilter projects={filter} value={project} onChange={onProjectChange} />
      )}
      {groups.map((group) => (
        <section key={group.id} aria-labelledby={`board-${group.id}`} className="py-1">
          <h2
            id={`board-${group.id}`}
            className="text-muted-foreground px-3 py-1 text-xs font-medium"
          >
            {group.title}
            <span aria-hidden className="ml-1.5 font-mono tabular-nums">
              {group.agents.length}
            </span>
          </h2>
          <ul>
            {group.agents.map((agent) => (
              <li key={agent.id}>
                <AgentRow agent={agent} project={names.get(agent.project_id ?? "")} />
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

type ProjectFilterProps = {
  projects: Project[];
  value: string;
  onChange: (project: string) => void;
};

/** ProjectFilter is a chip per project, and one for all of them. */
function ProjectFilter({ projects, value, onChange }: ProjectFilterProps) {
  const chip = (id: string, label: string) => (
    <button
      key={id}
      type="button"
      aria-pressed={value === id}
      className={cn(
        "hover:bg-accent focus-visible:ring-ring shrink-0 rounded-full border px-2.5 py-0.5 text-xs transition-colors focus-visible:ring-1 focus-visible:outline-none",
        value === id && "bg-accent font-medium",
      )}
      onClick={() => {
        onChange(id);
      }}
    >
      {label}
    </button>
  );
  return (
    <div
      role="group"
      aria-label="Project"
      className="flex gap-1.5 overflow-x-auto border-b px-3 py-2"
    >
      {chip("", "All")}
      {projects.map((p) => chip(p.id, p.name))}
    </div>
  );
}

type AgentRowProps = { agent: Session; project: string | undefined };

/** AgentRow is one subagent: its state, name, agent, project and when it last moved, and what it is doing. */
function AgentRow({ agent, project }: AgentRowProps) {
  const line = liveLine(agent);
  return (
    <NavLink
      to={`/activity/${encodeURIComponent(agent.id)}`}
      className="hover:bg-accent aria-[current=page]:bg-accent focus-visible:ring-ring flex min-h-11 min-w-0 flex-col justify-center gap-0.5 px-3 py-1.5 transition-colors focus-visible:ring-1 focus-visible:outline-none"
    >
      <span className="flex min-w-0 items-center gap-2">
        <StateDot state={agent.state} />
        <span className="sr-only">{stateLabel[agent.state]}: </span>
        <span className="min-w-0 truncate text-sm font-medium">{agent.name ?? agent.title}</span>
        <span className="text-muted-foreground min-w-0 truncate text-xs">
          {[agent.agent, project].filter((part) => part !== undefined && part !== "").join(" · ")}
        </span>
        <span className="text-muted-foreground ml-auto shrink-0 text-2xs">
          {formatAgo(agent.last_active_at)}
        </span>
      </span>
      {line.text !== "" && (
        <span
          className={cn(
            "min-w-0 truncate pl-4 text-xs",
            line.waiting ? "text-warning" : "text-muted-foreground",
          )}
        >
          {line.text}
        </span>
      )}
    </NavLink>
  );
}
