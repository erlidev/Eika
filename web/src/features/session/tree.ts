/**
 * The session tree as the rows the tree panel draws, and where the keyboard
 * moves between them. The rows are flat, each with its level, set size, and
 * position, which the WAI-ARIA tree pattern allows in place of nested groups:
 * the conversation reads straight down at one level, so its entries are
 * siblings on screen even though each is the child of the one before.
 */

import type { Node } from "@/api/types";

/** TreeRow is one entry as the tree shows it. */
export type TreeRow = {
  node: Node;
  /** level is the ARIA level, 1 for the conversation from the root. */
  level: number;
  /**
   * parent is the index of the row one level up that this one is drawn
   * under, or -1 at the top level.
   */
  parent: number;
  /** setSize and position place the row among the rows drawn beside it. */
  setSize: number;
  position: number;
};

/** childrenOf groups the outline by parent so it can be walked as a tree. */
function childrenOf(nodes: Node[]): Map<string, Node[]> {
  const byParent = new Map<string, Node[]>();
  for (const node of nodes) {
    const key = node.parent_id ?? "";
    byParent.set(key, [...(byParent.get(key) ?? []), node]);
  }
  return byParent;
}

/** headPath is the set of entries from the root to the head. */
function headPath(nodes: Node[], head: string | undefined): Set<string> {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const path = new Set<string>();
  for (let at = head; at !== undefined && !path.has(at); at = byId.get(at)?.parent_id) {
    path.add(at);
  }
  return path;
}

/**
 * treeRows lays the outline out in the order it is drawn. The path to the
 * head stays at one level. A branch off it is indented one level under the
 * entry it forks from and drawn before the conversation carries on, so a
 * long session does not walk off the panel and an abandoned branch never
 * looks like the latest.
 */
export function treeRows(nodes: Node[], head: string | undefined): TreeRow[] {
  const byParent = childrenOf(nodes);
  const onHeadPath = headPath(nodes, head);
  const rows: TreeRow[] = [];
  const seen = new Set<string>();

  const walk = (parentId: string, level: number, drawnUnder: number) => {
    const children = byParent.get(parentId) ?? [];
    const main =
      children.find((n) => onHeadPath.has(n.id)) ??
      (children.length === 1 ? children[0] : undefined);
    const ordered = [...children.filter((n) => n !== main), ...(main ? [main] : [])];
    for (const node of ordered) {
      if (seen.has(node.id)) continue;
      seen.add(node.id);
      const branch = node !== main && parentId !== "";
      const under = branch ? rowIndexOf(rows, parentId) : drawnUnder;
      const at = branch ? level + 1 : level;
      rows.push({ node, level: at, parent: under, setSize: 0, position: 0 });
      walk(node.id, at, under);
    }
  };
  walk("", 1, -1);

  // A row's set is the rows at its level drawn under the same row.
  const sets = new Map<string, TreeRow[]>();
  for (const row of rows) {
    const key = `${String(row.parent)}:${String(row.level)}`;
    sets.set(key, [...(sets.get(key) ?? []), row]);
  }
  for (const set of sets.values()) {
    set.forEach((row, i) => {
      row.setSize = set.length;
      row.position = i + 1;
    });
  }
  return rows;
}

function rowIndexOf(rows: TreeRow[], id: string): number {
  return rows.findIndex((r) => r.node.id === id);
}

/**
 * nextTreeIndex is where a key moves the focus from row `from`, or null for
 * a key the tree does not handle. Following the WAI-ARIA tree pattern: Up
 * and Down step through the rows as drawn, Home and End go to the ends,
 * Right goes to a row's first child, and Left to the row it is drawn under.
 * Nothing collapses, so Right and Left never expand or collapse.
 */
export function nextTreeIndex(rows: TreeRow[], from: number, key: string): number | null {
  if (rows.length === 0) return null;
  const at = rows[from];
  switch (key) {
    case "ArrowDown":
      return Math.min(from + 1, rows.length - 1);
    case "ArrowUp":
      return Math.max(from - 1, 0);
    case "Home":
      return 0;
    case "End":
      return rows.length - 1;
    case "ArrowRight": {
      const next = rows[from + 1];
      return at !== undefined && next !== undefined && next.level > at.level ? from + 1 : from;
    }
    case "ArrowLeft": {
      const up = at?.parent ?? -1;
      return up >= 0 ? up : from;
    }
    default:
      return null;
  }
}

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
