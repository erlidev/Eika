/** A chat in the sidebar. */

import { Archive, ArchiveRestore, MessageCircle, Pencil, Pin, PinOff, Trash2 } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";

import type { Session } from "@/api/types";
import { Row, RowFailure } from "@/app/SidebarRow";
import { useRenaming } from "@/app/useRenaming";
import type { RowAction } from "@/app/SidebarRow";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { useDeleteSession, useUpdateSession } from "@/features/sessions";

type SessionRowProps = {
  session: Session;
  /** indent is the left padding of the row, in pixels. */
  indent: number;
  sessionId?: string;
  onNavigate?: () => void;
};

/** SessionRow draws one chat with its menu: rename, pin, archive, delete. */
export function SessionRow({ session, indent, sessionId, onNavigate }: SessionRowProps) {
  const remove = useDeleteSession();
  const update = useUpdateSession();
  const navigate = useNavigate();
  const renaming = useRenaming();
  const [confirming, setConfirming] = useState(false);

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
        indent={indent}
        icon={<MessageCircle aria-hidden className="size-3.5 shrink-0" />}
        label={session.title}
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
      <RowFailure indent={indent} action={"change the chat"} error={update.error} />
      <RowFailure indent={indent} action={"delete the chat"} error={remove.error} />
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Delete ${session.title}?`}
        description="The chat's run is aborted and its entries are deleted."
        confirmLabel="Delete chat"
        onConfirm={() => {
          remove.mutate(session.id);
        }}
      />
    </li>
  );
}
