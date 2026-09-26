/** A session in the sidebar, with the forks and child agents that came out of it. */

import { Bot, GitBranch, MessageCircle, MessageSquare, Trash2 } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";

import type { Session } from "@/api/types";
import { IconButton } from "@/app/SidebarRow";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { useDeleteSession } from "@/features/sessions";
import type { SessionNode } from "@/features/sessions";
import { cn } from "@/lib/utils";

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

/**
 * SessionRow draws one session and, in a list of its own, the forks and child
 * agents that came out of it. The nesting is in the markup rather than only
 * in the indent, so the shape is there for a screen reader too.
 */
export function SessionRow({
  node,
  depth,
  indent,
  sessionId,
  onNavigate,
}: {
  node: SessionNode;
  /** depth is how many sessions this one hangs under; 0 is the user's own. */
  depth: number;
  /** indent is the left padding of a row at depth 0, in pixels. */
  indent: number;
  sessionId?: string;
  onNavigate?: () => void;
}) {
  const { session } = node;
  const current = session.id === sessionId;
  const remove = useDeleteSession();
  const navigate = useNavigate();
  const [confirming, setConfirming] = useState(false);
  return (
    <li>
      <div
        className={cn(
          "group hover:bg-accent flex items-center gap-1 rounded-md pr-1 transition-colors",
          current && "bg-accent",
        )}
      >
        <button
          type="button"
          aria-current={current ? "page" : undefined}
          className="focus-visible:ring-ring flex min-w-0 flex-1 items-center gap-1.5 py-1 text-left text-xs focus-visible:ring-1 focus-visible:outline-none"
          style={{ paddingLeft: `${String(indent + depth * 12)}px` }}
          onClick={() => {
            onNavigate?.();
            void navigate(`/sessions/${session.id}`);
          }}
        >
          {sessionIcon(session)}
          <span className="truncate">{session.title}</span>
          {session.kind !== "user" && (
            <span className="sr-only">{` (${sessionKindLabel(session.kind)})`}</span>
          )}
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
