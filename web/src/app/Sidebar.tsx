/**
 * The left pane: the chats and the sessions on the home computer, pinned ones
 * first, then newest first, with the archived ones folded at the end, and the
 * actions that start a new one. It is the only place that composes the list
 * features, so it lives in `app/` rather than in one of them.
 */

import { House, Plus } from "lucide-react";
import { useMemo } from "react";
import { useNavigate } from "react-router";

import type { CreateSession, Session } from "@/api/types";
import { ArchivedGroup } from "@/app/SidebarRow";
import { SessionRow } from "@/app/SidebarSession";
import { Button } from "@/components/ui/button";
import {
  splitArchived,
  useChats,
  useCreateSession,
  useHomeComputer,
  useHomeSessions,
} from "@/features/sessions";
import { failureText } from "@/lib/failure";

/**
 * newestFirst orders chats by when they were started, latest on top: the
 * list only grows, and the chat worth returning to is usually the last one.
 */
function newestFirst(chats: Session[]): Session[] {
  return [...chats].sort((a, b) => b.created_at.localeCompare(a.created_at));
}

export type SidebarProps = {
  /** sessionId is the open session, highlighted in the list. */
  sessionId?: string;
  /**
   * onNavigate is called when the list opens a chat. The narrow layout uses
   * it to close the drawer the list was drawn in.
   */
  onNavigate?: () => void;
};

export function Sidebar({ sessionId, onNavigate }: SidebarProps) {
  const chats = useChats();
  const home = useHomeComputer();
  const homeId = home.data?.id;
  const homeSessions = useHomeSessions(homeId);
  const create = useCreateSession();
  const navigate = useNavigate();
  const { active, archived } = useMemo(
    () => splitArchived(newestFirst([...(chats.data ?? []), ...(homeSessions.data ?? [])])),
    [chats.data, homeSessions.data],
  );
  const openNew = (input: CreateSession) => {
    create.mutate(input, {
      onSuccess: (session) => {
        onNavigate?.();
        void navigate(`/sessions/${session.id}`);
      },
    });
  };
  const row = (session: Session, indent: number) => (
    <SessionRow
      key={session.id}
      session={session}
      indent={indent}
      sessionId={sessionId}
      onNavigate={onNavigate}
    />
  );
  return (
    <nav aria-label="Chats" className="flex h-full min-h-0 flex-col">
      <header className="flex items-center gap-1 border-b px-2 py-1.5">
        <h2 className="flex-1 text-xs font-semibold tracking-wide uppercase">Chats</h2>
        {homeId !== undefined && (
          <Button
            size="icon"
            variant="ghost"
            className="size-6"
            aria-label="New home chat"
            title="A chat on the home computer, where the model can run commands and keep files"
            disabled={create.isPending}
            onClick={() => {
              openNew({ computer_id: homeId });
            }}
          >
            <House aria-hidden className="size-3.5" />
          </Button>
        )}
        <Button
          size="icon"
          variant="ghost"
          className="size-6"
          aria-label="New chat"
          disabled={create.isPending}
          onClick={() => {
            openNew({ chat: true });
          }}
        >
          <Plus aria-hidden className="size-3.5" />
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto p-1">
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
        {homeSessions.isError && (
          <p role="alert" className="text-destructive p-2 text-xs">
            {failureText("load the home computer's chats", homeSessions.error)}
          </p>
        )}
        {chats.data?.length === 0 && active.length === 0 && archived.length === 0 && (
          <p className="text-muted-foreground p-2 text-xs">No chats yet. Start one with +.</p>
        )}
        <ul>
          {active.map((session) => row(session, 8))}
          <ArchivedGroup count={archived.length} indent={8} what="chats">
            {archived.map((session) => row(session, 20))}
          </ArchivedGroup>
        </ul>
      </div>
    </nav>
  );
}
