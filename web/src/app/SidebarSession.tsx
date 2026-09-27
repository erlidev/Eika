/** A session in the sidebar, with the forks and child agents that came out of it. */

import {
  Archive,
  ArchiveRestore,
  Bot,
  GitBranch,
  MessageCircle,
  MessageSquare,
  Pencil,
  Pin,
  PinOff,
  Trash2,
} from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";

import type { Session } from "@/api/types";
import { Row, RowFailure } from "@/app/SidebarRow";
import { useRenaming } from "@/app/useRenaming";
import type { RowAction } from "@/app/SidebarRow";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { useDeleteSession, useUpdateSession } from "@/features/sessions";
import type { SessionNode } from "@/features/sessions";

/**
 * sessionIcon marks what a session is. A fork and a child agent are both
 * drawn under the session they came from, and the icon says which it is: the
 * indent alone cannot, and they behave differently. A chat has a round
 * bubble where a workspace's session has a square one.
 */
function sessionIcon(session: Session) {
  switch (session.kind) {
    case "fork":
      return <GitBranch aria-hidden className="size-3.5 shrink-0" />;
    case "agent":
      return <Bot aria-hidden className="size-3.5 shrink-0" />;
    default:
      return session.workspace_id === undefined ? (
        <MessageCircle aria-hidden className="size-3.5 shrink-0" />
      ) : (
        <MessageSquare aria-hidden className="size-3.5 shrink-0" />
      );
  }
}

/** deleteDescription says what deleting a session takes with it and what stays. */
function deleteDescription(session: Session): string {
  if (session.workspace_id === undefined) {
    return "The chat's run is aborted and its entries are deleted. Its forks stay.";
  }
  return session.kind === "agent"
    ? "The child agent's run is aborted and its entries are deleted. Its workspace and the branch it pushed stay."
    : "The session's run is aborted and its entries are deleted. The workspace and its files stay.";
}

/** sessionKindLabel names a session's kind for a screen reader. */
function sessionKindLabel(kind: Session["kind"]): string {
  switch (kind) {
    case "fork":
      return "fork";
    case "agent":
      return "agent";
    default:
      return "session";
  }
}

type SessionRowProps = {
  node: SessionNode;
  /** depth is how many sessions this one hangs under; 0 is the user's own. */
  depth: number;
  /** indent is the left padding of a row at depth 0, in pixels. */
  indent: number;
  sessionId?: string;
  onNavigate?: () => void;
};

/**
 * SessionRow draws one session and, in a list of its own, the forks and child
 * agents that came out of it. The nesting is in the markup rather than only
 * in the indent, so the shape is there for a screen reader too.
 */
export function SessionRow({ node, depth, indent, sessionId, onNavigate }: SessionRowProps) {
  const { session } = node;
  const remove = useDeleteSession();
  const update = useUpdateSession();
  const navigate = useNavigate();
  const renaming = useRenaming();
  const [confirming, setConfirming] = useState(false);
  const rowIndent = indent + depth * 12;
  const noun = session.workspace_id === undefined ? "chat" : "session";

  const menu: RowAction[] = [
    {
      label: "Rename",
      icon: Pencil,
      shortcut: "F2",
      takesFocus: true,
      onSelect: renaming.start,
    },
    {
      label: session.pinned ? "Unpin" : "Pin",
      icon: session.pinned ? PinOff : Pin,
      onSelect: () => {
        update.mutate({ id: session.id, changes: { pinned: !session.pinned } });
      },
    },
    {
      label: session.archived ? "Unarchive" : "Archive",
      icon: session.archived ? ArchiveRestore : Archive,
      onSelect: () => {
        update.mutate({ id: session.id, changes: { archived: !session.archived } });
      },
    },
    {
      label: "Delete",
      icon: Trash2,
      destructive: true,
      separated: true,
      onSelect: () => {
        setConfirming(true);
      },
    },
  ];

  return (
    <li>
      <Row
        indent={rowIndent}
        icon={sessionIcon(session)}
        label={session.title}
        labelNote={session.kind === "user" ? undefined : ` (${sessionKindLabel(session.kind)})`}
        current={session.id === sessionId}
        pinned={session.pinned}
        onClick={() => {
          onNavigate?.();
          void navigate(`/sessions/${session.id}`);
        }}
        menu={menu}
        renaming={renaming}
        onRename={(title) => {
          update.mutate({ id: session.id, changes: { title } });
        }}
      />
      <RowFailure indent={rowIndent} action={`change the ${noun}`} error={update.error} />
      <RowFailure indent={rowIndent} action={`delete the ${noun}`} error={remove.error} />
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Delete ${session.title}?`}
        description={deleteDescription(session)}
        confirmLabel="Delete session"
        onConfirm={() => {
          remove.mutate(session.id);
        }}
      />
      {node.children.length > 0 && (
        <ul>
          {node.children.map((child) => (
            <SessionRow
              key={child.session.id}
              node={child}
              depth={depth + 1}
              indent={indent}
              sessionId={sessionId}
              onNavigate={onNavigate}
            />
          ))}
        </ul>
      )}
    </li>
  );
}
