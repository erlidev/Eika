/**
 * The run panel: the run itself, what it is costing, the two message queues,
 * and the questions a run is blocked on. It is the place to answer a question
 * when the tool card has scrolled out of the transcript, and the place the
 * status bar's context meter explains itself.
 */

import { LoadError, Notice } from "@/components/Notice";
import { ContextBreakdown } from "@/features/session/ContextMeter";
import { AskUserBody } from "@/features/session/renderers/AskUserRenderer";
import { useRunStatus } from "@/features/session/queries";
import { useSessionStore } from "@/features/session/store";
import type { ToolItem } from "@/features/session/transcript";
import { formatAgo } from "@/lib/format";

export type RunPanelProps = {
  sessionId: string;
};

/** questionCall adapts a Question to the ToolItem the ask_user body reads. */
function questionCall(callId: string, question: string): ToolItem {
  return {
    kind: "tool",
    key: callId,
    runId: "",
    callId,
    name: "ask_user",
    arguments: { question },
    output: "",
    isError: false,
    done: false,
  };
}

export function RunPanel({ sessionId }: RunPanelProps) {
  const status = useRunStatus(sessionId);
  const meter = useSessionStore((s) => s.meter);
  if (status.isPending) {
    return (
      <div className="p-3">
        <Notice tone="pending">Loading the run…</Notice>
      </div>
    );
  }
  if (status.isError) {
    return (
      <div className="p-3">
        <LoadError
          what="the run status"
          error={status.error}
          retrying={status.isFetching}
          retry={() => void status.refetch()}
        />
      </div>
    );
  }
  const { active, run, pending_steering, pending_follow_ups, questions } = status.data;

  return (
    <div className="space-y-4 p-3 text-xs">
      <Section title="Run">
        {run ? (
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
            <dt className="text-muted-foreground">State</dt>
            <dd className="font-mono">{active ? "running" : run.state}</dd>
            <dt className="text-muted-foreground">Id</dt>
            <dd className="truncate font-mono">{run.id}</dd>
            <dt className="text-muted-foreground">Started</dt>
            <dd>{formatAgo(run.started_at)}</dd>
            {run.error !== undefined && run.error !== "" && (
              <>
                <dt className="text-muted-foreground">Error</dt>
                <dd className="text-destructive break-words">{run.error}</dd>
              </>
            )}
          </dl>
        ) : (
          <p className="text-muted-foreground">This session has not run yet.</p>
        )}
      </Section>

      <Section title="Context">
        <ContextBreakdown meter={meter} />
      </Section>

      <Section title={`Questions (${String(questions.length)})`}>
        {questions.length === 0 ? (
          <p className="text-muted-foreground">Nothing is waiting on you.</p>
        ) : (
          <ul className="space-y-3">
            {questions.map((question) => (
              <li key={question.id} className="rounded-md border p-2">
                <AskUserBody call={questionCall(question.call_id, question.question)} />
              </li>
            ))}
          </ul>
        )}
      </Section>

      <Section title={`Steering queue (${String(pending_steering.length)})`}>
        <Queue messages={pending_steering} />
      </Section>
      <Section title={`Follow-up queue (${String(pending_follow_ups.length)})`}>
        <Queue messages={pending_follow_ups} />
      </Section>
      {(pending_steering.length > 0 || pending_follow_ups.length > 0) && (
        <p className="text-muted-foreground italic">
          The API has no way to remove a queued message. Abort the run to discard the queues.
        </p>
      )}
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h3 className="text-muted-foreground mb-1 text-2xs font-semibold tracking-wide uppercase">
        {title}
      </h3>
      {children}
    </section>
  );
}

function Queue({ messages }: { messages: string[] }) {
  if (messages.length === 0) return <p className="text-muted-foreground">Empty.</p>;
  return (
    <ol className="space-y-1">
      {messages.map((message, index) => (
        <li key={`${String(index)}:${message}`} className="bg-muted rounded-md px-2 py-1">
          {message}
        </li>
      ))}
    </ol>
  );
}
