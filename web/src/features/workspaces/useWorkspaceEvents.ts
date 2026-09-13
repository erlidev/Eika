/**
 * The `workspace.state` subscription. A container's lifecycle is the one piece
 * of workspace state that changes without the user asking, so anything that
 * shows it subscribes here instead of polling.
 */

import { useQueryClient } from "@tanstack/react-query";

import { payloadOf, workspaceTopic } from "@/api/events";
import { queryKeys } from "@/api/keys";
import { useStreamSubscription } from "@/api/useStream";

/**
 * useWorkspaceEvents refreshes the cached workspace whenever it reaches a new
 * lifecycle state. Several components may call it for the same workspace; the
 * stream's reference counting keeps that to one subscription.
 */
export function useWorkspaceEvents(workspaceId: string | undefined): void {
  const client = useQueryClient();
  useStreamSubscription(
    workspaceId === undefined || workspaceId === "" ? undefined : workspaceTopic(workspaceId),
    (e) => {
      const payload = payloadOf(e, "workspace.state");
      if (!payload) return;
      void client.invalidateQueries({ queryKey: ["workspaces"] });
      void client.invalidateQueries({ queryKey: queryKeys.workspace(payload.workspace_id) });
    },
  );
}
