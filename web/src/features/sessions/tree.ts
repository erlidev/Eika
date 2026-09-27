/**
 * The session list of one workspace as the rows the sidebar draws. A fork and
 * a subagent are sessions in their own right, and both hang off the session
 * they came from: a fork of a conversation, and a child agent of the run that
 * spawned it. Drawing them flat loses that, and a fork or child with a
 * workspace of its own would read as an unrelated session in an unrelated
 * container.
 */

import type { Session } from "@/api/types";

/** SessionNode is one session with the sessions that came out of it. */
export type SessionNode = {
  session: Session;
  /** children are the forks and child agents that came out of it. */
  children: SessionNode[];
};

/** maxSessionDepth bounds the walk, whatever the rows say. */
const maxSessionDepth = 8;

/**
 * sessionTree nests a workspace's sessions: each one holds the forks and
 * child agents that came out of it, oldest first. The sidebar draws it as
 * nested lists, so the shape is in the markup and not only in the indent.
 *
 * Pinned sessions come first among their siblings; otherwise the order of
 * the list is kept.
 *
 * A session whose parent is not in the list is a root. That is what a child
 * of a session in another workspace looks like from here, and what is left of
 * a fork whose parent was deleted, since deleting a session clears the
 * pointer rather than removing the fork.
 */
export function sessionTree(sessions: Session[]): SessionNode[] {
  const listed = new Set(sessions.map((s) => s.id));
  const children = new Map<string, Session[]>();
  const roots: Session[] = [];
  for (const session of sessions) {
    const parent = session.parent_session_id;
    if (parent === undefined || parent === "" || !listed.has(parent)) {
      roots.push(session);
      continue;
    }
    children.set(parent, [...(children.get(parent) ?? []), session]);
  }

  const build = (session: Session, depth: number): SessionNode => ({
    session,
    children:
      depth >= maxSessionDepth
        ? []
        : pinnedFirst(children.get(session.id) ?? []).map((child) => build(child, depth + 1)),
  });
  return pinnedFirst(roots).map((root) => build(root, 0));
}

/**
 * pinnedFirst moves the pinned rows ahead of the others and keeps the order
 * within each group. Sessions and workspaces both sort this way.
 */
export function pinnedFirst<T extends { pinned: boolean }>(rows: readonly T[]): T[] {
  return [...rows.filter((r) => r.pinned), ...rows.filter((r) => !r.pinned)];
}

/**
 * splitArchived takes the archived sessions out of a tree for a section of
 * their own. An archived session leaves with everything under it, since a
 * fork of set-aside work is set aside too; one whose parent is still active
 * becomes a root of the archived list.
 */
export function splitArchived(nodes: SessionNode[]): {
  active: SessionNode[];
  archived: SessionNode[];
} {
  const archived: SessionNode[] = [];
  const keep = (list: SessionNode[]): SessionNode[] =>
    list.flatMap((node) => {
      if (node.session.archived) {
        archived.push(node);
        return [];
      }
      return [{ ...node, children: keep(node.children) }];
    });
  return { active: keep(nodes), archived };
}

/**
 * agentWorkspaces are the workspaces that exist to hold a fork or a child
 * agent rather than the user's own work: they hold sessions, none of them is
 * one the user opened, and every one of them hangs under a session that still
 * exists. The sidebar leaves those out of a project's workspace list, because
 * each is already reached through the session that owns it, and one thing
 * listed twice reads as two things.
 *
 * A workspace with no sessions is not one of them: a workspace the user has
 * just created has nothing in it yet and must still be listed. Nor is the
 * workspace of a fork that kept its parent's, since the parent's own session
 * is in it, nor one holding a session whose parent was deleted: deleting a
 * session clears its children's pointer, and hiding their workspace then
 * would leave them reachable from nowhere. Opening a session in a child
 * agent's workspace brings it back into the list, which is what adopting it
 * should do.
 */
export function agentWorkspaces(sessions: Session[]): Set<string> {
  const listed = new Set(sessions.map((s) => s.id));
  const hosting = new Set<string>();
  const kept = new Set<string>();
  for (const session of sessions) {
    // A chat is in no workspace, so it neither hides one nor keeps one.
    const workspace = session.workspace_id;
    if (workspace === undefined) continue;
    hosting.add(workspace);
    const parent = session.parent_session_id;
    const reachable = parent !== undefined && parent !== "" && listed.has(parent);
    if (session.kind === "user" || !reachable) kept.add(workspace);
  }
  return new Set([...hosting].filter((id) => !kept.has(id)));
}
