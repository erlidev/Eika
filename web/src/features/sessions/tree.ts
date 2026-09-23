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
        : (children.get(session.id) ?? []).map((child) => build(child, depth + 1)),
  });
  return roots.map((root) => build(root, 0));
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
    hosting.add(session.workspace_id);
    const parent = session.parent_session_id;
    const reachable = parent !== undefined && parent !== "" && listed.has(parent);
    if (session.kind === "user" || !reachable) kept.add(session.workspace_id);
  }
  return new Set([...hosting].filter((id) => !kept.has(id)));
}
