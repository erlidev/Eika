/** An MCP server's connection state, in the words and colours of the settings. */

import type { MCPServerState } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { stateLabels, stateTone } from "@/features/mcp/describe";
import { cn } from "@/lib/utils";

export function MCPStateBadge({ state }: { state: MCPServerState }) {
  return (
    <Badge
      variant="outline"
      className={cn("h-4 shrink-0 px-1 font-mono text-2xs", stateTone[state])}
    >
      {stateLabels[state]}
    </Badge>
  );
}
