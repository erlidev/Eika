/**
 * One subagent: its header with the state and Cancel, its brief, its session
 * watched live with nothing to type into, and its report once it has one.
 * Steering goes through Eika, so the view says so and links to Chat.
 */

import { ArrowLeft, ChevronRight } from "lucide-react";
import { Link } from "react-router";

import { ApiError } from "@/api/client";
import type { Session } from "@/api/types";
import { ActionError, LoadError, Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { StateDot } from "@/features/activity/StateDot";
import { useCancelSubagent, useProjects } from "@/features/activity/queries";
import { firstLine, isActive, isSubagent, liveLine, stateLabel } from "@/features/activity/state";
import { SessionView, useSession } from "@/features/session";

export type AgentDetailProps = { sessionId: string };

export function AgentDetail({ sessionId }: AgentDetailProps) {
  const session = useSession(sessionId);
  if (session.isPending) {
    return (
      <div className="p-3">
        <Notice tone="pending">Loading the subagent…</Notice>
      </div>
    );
  }
  if (session.isError) {
    const gone = session.error instanceof ApiError && session.error.status === 404;
    return (
      <div className="flex flex-col gap-3 p-3">
        <BackLink />
        {gone ? (
          <Notice>That subagent does not exist.</Notice>
        ) : (
          <LoadError
            what="the subagent"
            error={session.error}
            retry={() => void session.refetch()}
            retrying={session.isFetching}
          />
        )}
      </div>
    );
  }
  const agent = session.data.session;
  if (!isSubagent(agent)) {
    return (
      <div className="flex flex-col gap-3 p-3">
        <BackLink />
        <Notice>That session is not a subagent.</Notice>
      </div>
    );
  }
  return (
    <>
      <Header agent={agent} />
      <Brief brief={agent.brief ?? ""} />
      <SessionView key={agent.id} sessionId={agent.id} readOnly />
      {(agent.result ?? "") !== "" && (
        <section
          aria-label="Report"
          className="bg-card m-2 max-h-48 shrink-0 overflow-y-auto rounded-md border px-3 py-2"
        >
          <h3 className="text-xs font-medium">Report</h3>
          <p className="mt-1 text-sm break-words whitespace-pre-wrap">{agent.result}</p>
        </section>
      )}
    </>
  );
}

/** BackLink returns to the board; on a screen that shows both, there is nothing to return to. */
function BackLink() {
  return (
    <Link
      to="/activity"
      className="text-muted-foreground hover:text-foreground flex w-fit items-center gap-1 text-xs transition-colors lg:hidden"
    >
      <ArrowLeft aria-hidden className="size-3.5" />
      Activity
    </Link>
  );
}

type HeaderProps = { agent: Session };

/** Header names the subagent, says what state it is in and why, and offers Cancel while it is running or waiting. */
function Header({ agent }: HeaderProps) {
  const projects = useProjects();
  const cancel = useCancelSubagent(agent.id);
  const project = projects.data?.find((p) => p.id === agent.project_id)?.name;
  return (
    <header className="border-b">
      <div className="flex items-start gap-2 px-3 py-2">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <BackLink />
          <h2 className="truncate text-sm font-semibold">{agent.name ?? agent.title}</h2>
          <p className="text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs">
            <span className="text-foreground flex items-center gap-1.5">
              <StateDot state={agent.state} />
              {stateLabel[agent.state]}
            </span>
            {agent.agent !== undefined && <span>{agent.agent}</span>}
            {project !== undefined && <span>{project}</span>}
          </p>
          {agent.state === "waiting" && (agent.waiting_reason ?? "") !== "" && (
            <p className="text-warning text-xs">{liveLine(agent).text}</p>
          )}
        </div>
        {isActive(agent.state) && (
          <Button
            size="sm"
            variant="destructive"
            disabled={cancel.isPending}
            onClick={() => {
              cancel.mutate();
            }}
          >
            Cancel
          </Button>
        )}
      </div>
      {cancel.isError && (
        <ActionError action="cancel the subagent" error={cancel.error} className="mx-3 mb-2" />
      )}
      <p className="text-muted-foreground bg-muted/50 border-t px-3 py-1.5 text-xs">
        To steer this, tell Eika in{" "}
        <Link to="/" className="text-primary underline-offset-2 hover:underline">
          Chat
        </Link>
        .
      </p>
    </header>
  );
}

type BriefProps = { brief: string };

/** Brief is what the subagent was asked to do, folded to its first line. */
function Brief({ brief }: BriefProps) {
  if (brief === "") return null;
  return (
    <Collapsible className="group border-b">
      <CollapsibleTrigger className="hover:bg-accent focus-visible:ring-ring flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-xs transition-colors focus-visible:ring-1 focus-visible:outline-none">
        <ChevronRight
          aria-hidden
          className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-90"
        />
        <span className="font-medium">Brief</span>
        <span className="text-muted-foreground truncate group-data-[state=open]:hidden">
          {firstLine(brief)}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <p className="max-h-40 overflow-y-auto px-3 pb-2 pl-8 text-sm break-words whitespace-pre-wrap">
          {brief}
        </p>
      </CollapsibleContent>
    </Collapsible>
  );
}
