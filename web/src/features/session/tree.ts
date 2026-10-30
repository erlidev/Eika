/** Where a rewind lands in a session's tree of entries. */

import type { Node } from "@/api/types";

/**
 * rewindTarget is the entry the head moves to when the user rewinds to a
 * message they sent: the one before it, so that sending again replaces that
 * turn rather than following it. An empty string is the session's start,
 * which is what rewinding to the first message means.
 *
 * It returns null when there is nothing to rewind to: an entry the outline
 * does not hold, or one whose parent is in the middle of a turn and so leaves
 * tool calls unanswered. The harness refuses that head, so the message offers
 * no rewind rather than failing on the click.
 */
export function rewindTarget(nodes: Node[], entryId: string): string | null {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const node = byId.get(entryId);
  if (node === undefined) return null;
  const parentId = node.parent_id;
  if (parentId === undefined || parentId === "") return "";
  const parent = byId.get(parentId);
  if (parent?.resumable !== true) return null;
  return parent.id;
}
