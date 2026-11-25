/**
 * What the board says about a subagent: the group its state puts it in, the
 * words and colour of the state, and the one line under its name. Pure, so
 * the ordering and the wording are tested without a page.
 */

import type { Session, SessionState } from "@/api/types";

/** BoardGroupId names a group of the board. */
export type BoardGroupId = "waiting" | "running" | "idle" | "stopped";

/** BoardGroup is one heading of the board and the subagents under it. */
export type BoardGroup = { id: BoardGroupId; title: string; agents: Session[] };

/** groupOrder is the order of the groups: what needs a look first. */
const groupOrder: readonly { id: BoardGroupId; title: string; states: SessionState[] }[] = [
  { id: "waiting", title: "Waiting", states: ["waiting"] },
  { id: "running", title: "Running", states: ["running"] },
  { id: "idle", title: "Idle", states: ["idle"] },
  { id: "stopped", title: "Failed and cancelled", states: ["failed", "cancelled"] },
];

/** stateLabel is the state in words. */
export const stateLabel: Record<SessionState, string> = {
  idle: "Idle",
  running: "Running",
  waiting: "Waiting",
  failed: "Failed",
  cancelled: "Cancelled",
};

/** stateDot is the colour class of a state's dot: theme tokens only. */
export const stateDot: Record<SessionState, string> = {
  idle: "bg-muted-foreground",
  running: "bg-primary",
  waiting: "bg-warning",
  failed: "bg-destructive",
  cancelled: "bg-muted-foreground",
};

/** isSubagent is whether a session is one of Eika's subagents. */
export function isSubagent(session: Session): boolean {
  return session.kind === "subagent";
}

/** isActive is whether a subagent has a run to cancel: it is running or waiting. */
export function isActive(state: SessionState): boolean {
  return state === "running" || state === "waiting";
}

/**
 * boardGroups sorts the subagents into the board's groups, the most recently
 * active first, and leaves out the groups that are empty. `project` narrows
 * the board to one project's subagents; empty keeps all of them.
 */
export function boardGroups(agents: readonly Session[], project: string): BoardGroup[] {
  const shown = project === "" ? agents : agents.filter((a) => a.project_id === project);
  return groupOrder
    .map(({ id, title, states }) => ({
      id,
      title,
      agents: shown
        .filter((a) => states.includes(a.state))
        .sort(
          (a, b) => b.last_active_at.localeCompare(a.last_active_at) || a.id.localeCompare(b.id),
        ),
    }))
    .filter((group) => group.agents.length > 0);
}

/**
 * projectsOf lists the projects the subagents belong to, in the order of the
 * project list, for the board's filter.
 */
export function projectsOf<P extends { id: string }>(
  agents: readonly Session[],
  projects: readonly P[],
): P[] {
  const used = new Set(agents.map((a) => a.project_id));
  return projects.filter((p) => used.has(p.id));
}

/** firstLine is the first non-empty line of a text. */
export function firstLine(text: string | undefined): string {
  return (
    (text ?? "")
      .split(/\r?\n/)
      .map((line) => line.trim())
      .find((line) => line !== "") ?? ""
  );
}

/** LiveLine is the line under a subagent's name. */
export type LiveLine = { text: string; waiting: boolean };

/**
 * liveLine says what a subagent is doing, as far as its row knows: the
 * reason it waits, else the first line of its latest report, else the start of
 * its brief. The stream of what it is doing step by step is in its own view.
 */
export function liveLine(agent: Session): LiveLine {
  if (agent.state === "waiting") {
    const reason = agent.waiting_reason ?? "";
    return { text: reason === "" ? "Waiting" : `Waiting on ${reason}`, waiting: true };
  }
  const report = firstLine(agent.result);
  if (report !== "") return { text: report, waiting: false };
  return { text: firstLine(agent.brief), waiting: false };
}
