/**
 * The application root: the connect screen until a token is stored, and the
 * workbench once one is. Routing is by session, so a session has a URL that
 * survives a reload.
 */

import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { Navigate, Route, Routes, useParams } from "react-router";

import { resetEventStream } from "@/api/stream";
import { Workbench } from "@/app/Workbench";
import { ConnectScreen, useConnection } from "@/features/connect";
import { useSessions } from "@/features/sessions";

export function App() {
  const connection = useConnection();
  const client = useQueryClient();
  const connected = connection.token !== "";

  // A token that changed makes every cached answer and the open socket stale:
  // the socket carries the old token in its URL and cannot be re-authorised.
  useEffect(() => {
    if (connected) return;
    resetEventStream();
    client.clear();
  }, [connected, client]);

  if (!connected) return <ConnectScreen />;

  return (
    <Routes>
      <Route path="/" element={<Workbench />} />
      <Route path="/sessions/:sessionId" element={<Workbench />} />
      <Route path="/workspaces/:workspaceId" element={<WorkspaceRoute />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

/**
 * WorkspaceRoute opens a workspace's newest session. A workspace is not a view
 * of its own: everything in it is reached through a session.
 */
function WorkspaceRoute() {
  const params = useParams();
  const sessions = useSessions(params.workspaceId);
  if (params.workspaceId === undefined) return <Navigate to="/" replace />;
  if (sessions.isPending) return <Workbench />;
  const newest = sessions.data?.[0];
  return newest ? <Navigate to={`/sessions/${newest.id}`} replace /> : <Workbench />;
}
