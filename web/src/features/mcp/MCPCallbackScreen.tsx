/**
 * The page an OAuth authorization server sends the browser back to after
 * the user signed in to an MCP server. It hands what came back to the
 * harness, which redeems the code, and then returns to the workbench on the
 * server's settings. No harness route opens for the redirect: the page posts
 * to the authenticated API, so the sign-in token stays in the browser.
 */

import { CircleAlert, KeyRound } from "lucide-react";
import { useEffect, useRef } from "react";

import { Notice } from "@/components/Notice";
import { Screen, ScreenHeader, ScreenMark } from "@/components/Screen";
import { Button } from "@/components/ui/button";
import { callbackFromQuery } from "@/features/mcp/callback";
import { useFinishMCPAuthorization } from "@/features/mcp/queries";
import { failureText } from "@/lib/failure";

export type MCPCallbackScreenProps = {
  /** search is the page's query string, as the authorization server wrote it. */
  search: string;
  /** onDone returns to the workbench, on the server that was authorized when there is one. */
  onDone: (serverId?: string) => void;
};

export function MCPCallbackScreen({ search, onDone }: MCPCallbackScreenProps) {
  const finish = useFinishMCPAuthorization();
  const callback = callbackFromQuery(search);
  // An authorization is finished once: a second post of the same state is
  // refused, so the page must not send it again when it renders again.
  const sent = useRef(false);
  const latest = useRef(onDone);
  useEffect(() => {
    latest.current = onDone;
  });

  useEffect(() => {
    if (callback === null || sent.current) return;
    sent.current = true;
    finish.mutate(callback, {
      onSuccess: (serverId) => {
        latest.current(serverId);
      },
    });
  }, [callback, finish]);

  if (callback !== null && !finish.isError) {
    return (
      <Screen className="space-y-4">
        <ScreenHeader mark={<ScreenMark icon={KeyRound} />} title="Signing in">
          <p>
            The harness is finishing the sign-in with the MCP server&apos;s authorization server.
          </p>
        </ScreenHeader>
        <Notice tone="pending">Redeeming the authorization…</Notice>
      </Screen>
    );
  }
  return (
    <Screen role="alert" className="space-y-4">
      <ScreenHeader
        mark={<ScreenMark icon={CircleAlert} tone="error" />}
        title="The sign-in did not finish"
      >
        <p className="text-foreground">
          {callback === null
            ? "This page is where an authorization server sends the browser back, and it came here with nothing to finish."
            : failureText("finish signing in to the MCP server", finish.error)}
        </p>
        <p className="mt-2 text-xs">Start again from the server in Settings, MCP.</p>
      </ScreenHeader>
      <Button
        type="button"
        className="w-full"
        onClick={() => {
          onDone();
        }}
      >
        Back to Eika
      </Button>
    </Screen>
  );
}
