/**
 * A workspace's lifecycle state, kept current by the `workspace.state` event
 * rather than by polling.
 */

import { Badge } from "@/components/ui/badge";
import { useWorkspaceEvents } from "@/features/workspaces/useWorkspaceEvents";
import { cn } from "@/lib/utils";

export type WorkspaceStateBadgeProps = {
  workspaceId: string;
  state: string;
};

const tone: Record<string, string> = {
  running: "border-success/40 text-success",
  creating: "border-warning/40 text-warning",
  stopped: "text-muted-foreground",
  gone: "border-destructive/40 text-destructive",
};

export function WorkspaceStateBadge({ workspaceId, state }: WorkspaceStateBadgeProps) {
  useWorkspaceEvents(workspaceId);
  return (
    <Badge
      variant="outline"
      className={cn("h-4 px-1 font-mono text-2xs", tone[state])}
      aria-label={`Workspace state: ${state}`}
    >
      {state}
    </Badge>
  );
}
