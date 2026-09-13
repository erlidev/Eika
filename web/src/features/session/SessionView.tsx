/**
 * The centre pane: one session's transcript, its run status, and the
 * composer. It owns the session's stream subscription.
 */

import { useEffect, useState } from "react";

import { Composer } from "@/features/session/Composer";
import { useAbortRun, useRunStatus, useSession } from "@/features/session/queries";
import { RunStatusBar } from "@/features/session/RunStatusBar";
import { Transcript } from "@/features/session/Transcript";
import { useSessionStream } from "@/features/session/useSessionStream";
import { defaultModelOf, useSettings } from "@/features/settings";
import { useWorkspace, useWorkspaceEvents } from "@/features/workspaces";

export type SessionViewProps = {
  sessionId: string;
};

export function SessionView({ sessionId }: SessionViewProps) {
  useSessionStream(sessionId);
  const session = useSession(sessionId);
  const settings = useSettings();
  const status = useRunStatus(sessionId);
  const abort = useAbortRun(sessionId);
  const [model, setModel] = useState("");

  const workspaceId = session.data?.session.workspace_id;
  useWorkspaceEvents(workspaceId);
  const workspace = useWorkspace(workspaceId);
  // Until the workspace is known the composer stays usable: the harness
  // rejects a run on a stopped workspace, and guessing would be worse.
  const running = workspace.data === undefined || workspace.data.state === "running";

  // Esc aborts the run from anywhere in the session view, which is why the
  // listener is on the document rather than on one control.
  const run = status.data?.run;
  const active = status.data?.active ?? false;
  useEffect(() => {
    if (!active || !run) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") abort.mutate(run.id);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [active, run, abort]);

  const chosenModel = model === "" ? defaultModelOf(settings.data) : model;

  return (
    <section className="flex min-h-0 flex-1 flex-col" aria-label="Session">
      <header className="flex items-center gap-2 border-b px-3 py-2">
        <h2 className="truncate text-sm font-semibold">
          {session.data?.session.title ?? "Session"}
        </h2>
        <span className="text-muted-foreground shrink-0 font-mono text-xs">{sessionId}</span>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <Transcript empty="Send a message to start the first turn." />
      </div>
      <RunStatusBar sessionId={sessionId} model={chosenModel} onModelChange={setModel} />
      <Composer
        sessionId={sessionId}
        model={chosenModel}
        disabled={!running}
        disabledReason="The workspace is not running; start it to send a message."
      />
    </section>
  );
}
