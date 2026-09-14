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
  running: "border-emerald-500/40 text-emerald-700 dark:text-emerald-400",
  creating: "border-amber-500/40 text-amber-700 dark:text-amber-400",
  stopped: "text-muted-foreground",
  gone: "border-destructive/40 text-destructive",
};

export function WorkspaceStateBadge({ workspaceId, state }: WorkspaceStateBadgeProps) {
  useWorkspaceEvents(workspaceId);
  return (
    <Badge
      variant="outline"
      className={cn("h-4 px-1 font-mono text-[0.65rem]", tone[state])}
      aria-label={`Workspace state: ${state}`}
    >
      {state}
    </Badge>
  );
}
