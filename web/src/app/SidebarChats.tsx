/**
 * The sidebar's Chats section: the sessions in no workspace, pinned ones
 * first, then newest first, with the archived ones folded at the end.
 */

import { Plus } from "lucide-react";
import { useMemo } from "react";
import { useNavigate } from "react-router";

import type { Session } from "@/api/types";
import { ArchivedGroup } from "@/app/SidebarRow";
import { SessionRow } from "@/app/SidebarSession";
import { Button } from "@/components/ui/button";
import { sessionTree, splitArchived, useChats, useCreateSession } from "@/features/sessions";
import { failureText } from "@/lib/failure";

/**
 * newestFirst orders chats by when they were started, latest on top: the
 * list only grows, and the chat worth returning to is usually the last one.
 */
function newestFirst(chats: Session[]): Session[] {
  return [...chats].sort((a, b) => b.created_at.localeCompare(a.created_at));
}

type ChatListProps = {
  sessionId?: string;
  onNavigate?: () => void;
};

/**
 * ChatList is the sidebar's second section: the sessions with no workspace,
 * each with the forks made of it. It is apart from the projects, under a
 * heading of its own, so that a chat is never mistaken for work in a
 * workspace. It takes at most two fifths of the pane and scrolls inside that.
 */
export function ChatList({ sessionId, onNavigate }: ChatListProps) {
  const chats = useChats();
  const create = useCreateSession();
  const navigate = useNavigate();
  const { active, archived } = useMemo(
    () => splitArchived(sessionTree(newestFirst(chats.data ?? []))),
    [chats.data],
  );
  const row = (node: (typeof active)[number], indent: number) => (
    <SessionRow
      key={node.session.id}
      node={node}
      depth={0}
      indent={indent}
      sessionId={sessionId}
      onNavigate={onNavigate}
    />
  );
  return (
    <nav aria-label="Chats" className="flex max-h-2/5 shrink-0 flex-col border-t">
      <header className="flex items-center gap-1 border-b px-2 py-1.5">
        <h2 className="flex-1 text-xs font-semibold tracking-wide uppercase">Chats</h2>
        <Button
          size="icon"
          variant="ghost"
          className="size-6"
          aria-label="New chat"
          disabled={create.isPending}
          onClick={() => {
            create.mutate(
              { chat: true },
              {
                onSuccess: (chat) => {
                  onNavigate?.();
                  void navigate(`/sessions/${chat.id}`);
                },
              },
            );
          }}
        >
          <Plus aria-hidden className="size-3.5" />
        </Button>
      </header>
      <div className="min-h-0 overflow-y-auto p-1">
        {create.isError && (
          <p role="alert" className="text-destructive p-2 text-xs">
            {failureText("start a chat", create.error)}
          </p>
        )}
        {chats.isPending && <p className="text-muted-foreground p-2 text-xs">Loading…</p>}
        {chats.isError && (
          <p role="alert" className="text-destructive p-2 text-xs">
            {failureText("load the chats", chats.error)}
          </p>
        )}
        {chats.data?.length === 0 && (
          <p className="text-muted-foreground p-2 text-xs">
            No chats yet. A chat talks to a model with no workspace.
          </p>
        )}
        <ul>
          {active.map((node) => row(node, 8))}
          <ArchivedGroup count={archived.length} indent={8} what="chats">
            {archived.map((node) => row(node, 20))}
          </ArchivedGroup>
        </ul>
      </div>
    </nav>
  );
}
