/**
 * The Chat page: the one conversation with Eika, which is the home session.
 * It finds the session and hands it to the session view, which streams it and
 * owns the transcript and the composer.
 */

import { LoadError, Notice } from "@/components/Notice";
import { useHome } from "@/features/chat/queries";
import { SessionView } from "@/features/session";

export function ChatPage() {
  const home = useHome();
  if (home.isPending) {
    return (
      <div className="p-3">
        <Notice tone="pending">Loading the conversation…</Notice>
      </div>
    );
  }
  if (home.isError) {
    return (
      <div className="p-3">
        <LoadError
          what="the conversation"
          error={home.error}
          retry={() => void home.refetch()}
          retrying={home.isFetching}
        />
      </div>
    );
  }
  return <SessionView key={home.data.session.id} sessionId={home.data.session.id} />;
}
