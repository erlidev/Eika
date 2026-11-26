/**
 * The Activity page: the board of Eika's subagents beside a read-only live
 * view of the one that is open. From 1024px both show; under it the route
 * picks one, so a phone goes from the board to a subagent and back.
 */

import { useState } from "react";
import { useParams } from "react-router";

import { AgentBoard } from "@/features/activity/AgentBoard";
import { AgentDetail } from "@/features/activity/AgentDetail";
import { ScheduleActivity } from "@/features/schedules";
import { cn } from "@/lib/utils";

export function ActivityPage() {
  const { sessionId } = useParams();
  const [project, setProject] = useState("");
  const open = sessionId !== undefined;
  return (
    <div className="flex min-h-0 flex-1">
      <nav
        aria-label="Subagents"
        className={cn(
          "min-h-0 min-w-0 flex-1 flex-col lg:flex lg:w-80 lg:flex-none lg:border-r",
          open ? "hidden" : "flex",
        )}
      >
        <AgentBoard project={project} onProjectChange={setProject} />
        <ScheduleActivity />
      </nav>
      <div className={cn("min-h-0 min-w-0 flex-1 flex-col lg:flex", open ? "flex" : "hidden")}>
        {sessionId === undefined ? (
          <p className="text-muted-foreground m-auto p-4 text-sm">
            Pick a subagent to watch it work.
          </p>
        ) : (
          <AgentDetail key={sessionId} sessionId={sessionId} />
        )}
      </div>
    </div>
  );
}
