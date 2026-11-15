/**
 * The application root. A browser that is not signed in gets the guided
 * setup when the harness is new and the sign-in screen when it is not. A
 * signed-in browser gets the setup until it is finished, then the shell, whose
 * one page today is Chat. The workbench stays in the tree and has no route.
 */

import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router";

import { ApiError } from "@/api/client";
import { resetEventStream } from "@/api/stream";
import { Shell } from "@/app/Shell";
import { Splash } from "@/components/Splash";
import { ChatPage } from "@/features/chat";
import { LoadFailedScreen, SignInScreen, useAuthStatus, useConnection } from "@/features/connect";
import { callbackPath, MCPCallbackScreen, useMCPSelection } from "@/features/mcp";
import { setupComplete, useSettings, useSettingsDialog } from "@/features/settings";
import { SetupWizard } from "@/features/setup";

export function App() {
  const connection = useConnection();
  const client = useQueryClient();
  const connected = connection.token !== "";

  // A token that changed makes every cached answer and the open socket stale:
  // the socket carries the old token in its URL and cannot be re-authorised.
  // Whether the harness is set up needs no token, so that answer stays.
  useEffect(() => {
    if (connected) return;
    resetEventStream();
    client.removeQueries({ predicate: (query) => query.queryKey[0] !== "auth" });
  }, [connected, client]);

  return connected ? <SignedIn /> : <SignedOut />;
}

/** SignedOut offers setup to a new harness and sign-in to one that is set up. */
function SignedOut() {
  const status = useAuthStatus();
  if (status.isPending) return <Splash />;
  if (status.isError) {
    return (
      <SignInScreen
        unreachable={status.error.message}
        retry={() => void status.refetch()}
        retrying={status.isFetching}
      />
    );
  }
  return status.data.password_set ? <SignInScreen /> : <SetupWizard />;
}

/** SignedIn finishes the setup first, then serves the shell. */
function SignedIn() {
  const settings = useSettings();
  const status = useAuthStatus();
  if (settings.isPending || status.isPending) return <Splash />;
  // A refused token disconnects in the client, which leaves this branch for
  // the sign-in screen. Any other failure is the harness's: the browser is
  // still signed in, so it gets the error and Retry, never a password form.
  // Only a first load that failed gets there: a refetch that fails later,
  // such as the one after a model is deleted, keeps the settings already
  // loaded rather than tear down the shell and whatever is open in it.
  if (settings.isError && settings.data === undefined) {
    if (settings.error instanceof ApiError && settings.error.status === 401) return <Splash />;
    return (
      <LoadFailedScreen
        what="the settings"
        error={settings.error}
        retry={() => void settings.refetch()}
        retrying={settings.isFetching}
      />
    );
  }
  if (status.data?.password_set === false || !setupComplete(settings.data)) {
    return <SetupWizard />;
  }

  return (
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<ChatPage />} />
      </Route>
      <Route path={callbackPath} element={<MCPCallbackRoute />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

/**
 * MCPCallbackRoute is where an OAuth authorization server returns the
 * browser. Once the harness has the answer, the shell opens again with
 * the settings on the server that was signed in to.
 */
function MCPCallbackRoute() {
  const { search } = useLocation();
  const navigate = useNavigate();
  const showSettings = useSettingsDialog((s) => s.show);
  const select = useMCPSelection((s) => s.select);
  return (
    <MCPCallbackScreen
      search={search}
      onDone={(serverId) => {
        if (serverId !== undefined) select(serverId);
        showSettings("mcp");
        void navigate("/", { replace: true });
      }}
    />
  );
}
