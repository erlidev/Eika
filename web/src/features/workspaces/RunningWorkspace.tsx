/**
 * The gate every panel that works inside a sandbox sits behind: its content
 * when the workspace is running, and otherwise what state it is in, with a
 * Start button when starting it is what the user needs.
 */

import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { useWorkspace, useWorkspaceAction } from "@/features/workspaces/queries";
import { useWorkspaceEvents } from "@/features/workspaces/useWorkspaceEvents";

export type RunningWorkspaceProps = {
  workspaceId: string;
  /** purpose says what the panel needs the sandbox for: "browse its files". */
  purpose: string;
  children: React.ReactNode;
};

export function RunningWorkspace({ workspaceId, purpose, children }: RunningWorkspaceProps) {
  useWorkspaceEvents(workspaceId);
  const workspace = useWorkspace(workspaceId);
  const action = useWorkspaceAction();

  if (workspace.isPending) {
    return (
      <div className="p-3">
        <Notice tone="pending">Loading the workspace…</Notice>
      </div>
    );
  }
  if (workspace.isError) {
    return (
      <div className="p-3">
        <LoadError
          what="the workspace"
          error={workspace.error}
          retrying={workspace.isFetching}
          retry={() => void workspace.refetch()}
        />
      </div>
    );
  }
  switch (workspace.data.state) {
    case "running":
      return children;
    case "creating":
      return (
        <div className="p-3">
          <Notice tone="pending">The workspace is starting…</Notice>
        </div>
      );
    case "gone":
      return (
        <div className="p-3">
          <Notice tone="error">
            The workspace&apos;s container is gone, so there is nothing to {purpose} in.
          </Notice>
        </div>
      );
    case "stopped":
      return (
        <div className="space-y-2 p-3">
          <Notice
            action={
              <Button
                type="button"
                size="xs"
                disabled={action.isPending}
                onClick={() => {
                  action.mutate({ id: workspaceId, action: "start" });
                }}
              >
                {action.isPending ? "Starting…" : "Start"}
              </Button>
            }
          >
            The workspace is stopped. Start it to {purpose}.
          </Notice>
          {action.isError && <ActionError action="start the workspace" error={action.error} />}
        </div>
      );
  }
}
