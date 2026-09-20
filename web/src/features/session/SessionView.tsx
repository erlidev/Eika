/**
 * The centre pane: one session's transcript, its run status, and the
 * composer. It owns the session's stream subscription.
 */

import { useEffect } from "react";

import { Composer } from "@/features/session/Composer";
import { abortsRun } from "@/features/session/escape";
import { useAbortRun, useRunStatus, useSession } from "@/features/session/queries";
import { RunStatusBar } from "@/features/session/RunStatusBar";
import { Transcript } from "@/features/session/Transcript";
import { useSessionStore } from "@/features/session/store";
import { useSessionStream } from "@/features/session/useSessionStream";
import { Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { useModels } from "@/features/providers";
import { useSettingsDialog } from "@/features/settings";
import { useWorkspace, useWorkspaceEvents } from "@/features/workspaces";

export type SessionViewProps = {
  sessionId: string;
};

export function SessionView({ sessionId }: SessionViewProps) {
  useSessionStream(sessionId);
  const session = useSession(sessionId);
  const models = useModels();
  const showSettings = useSettingsDialog((s) => s.show);
  const status = useRunStatus(sessionId);
  const abort = useAbortRun(sessionId);
  const model = useSessionStore((s) => s.model);
  const chooseModel = useSessionStore((s) => s.chooseModel);

  const workspaceId = session.data?.session.workspace_id;
  useWorkspaceEvents(workspaceId);
  const workspace = useWorkspace(workspaceId);
  // Until the workspace is known the composer stays usable: the harness
  // rejects a run on a stopped workspace, and guessing would be worse.
  const running = workspace.data === undefined || workspace.data.state === "running";

  // Esc aborts the run from anywhere in the session view, which is why the
  // listener is on the document rather than on one control. `abortsRun`
  // leaves the presses that belong to a dialog or a text field alone.
  const run = status.data?.run;
  const active = status.data?.active ?? false;
  useEffect(() => {
    if (!active || !run) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || !abortsRun(e)) return;
      abort.mutate(run.id);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [active, run, abort]);

  const chosenModel = model === "" ? (models.data?.default ?? "") : model;
  const noModel = models.data?.models.length === 0;

  return (
    <section className="flex min-h-0 flex-1 flex-col" aria-label="Session">
      <header className="flex items-center gap-2 border-b px-3 py-2">
        <h2 className="truncate text-sm font-semibold">
          {session.data?.session.title ?? "Session"}
        </h2>
        <span className="text-muted-foreground shrink-0 font-mono text-xs">{sessionId}</span>
      </header>
      {/* The transcript owns its scroller: it has to measure it to decide
          whether the user is still following the stream. */}
      <Transcript empty="Send a message to start the first turn." />
      {noModel && (
        <div className="border-t px-3 py-2">
          <Notice className="mx-auto max-w-3xl">
            <span className="flex flex-wrap items-center justify-between gap-2">
              No model is configured yet, so the agent has nothing to run on.
              <Button
                size="xs"
                variant="outline"
                onClick={() => {
                  showSettings("models");
                }}
              >
                Add a model
              </Button>
            </span>
          </Notice>
        </div>
      )}
      <RunStatusBar sessionId={sessionId} model={chosenModel} onModelChange={chooseModel} />
      <Composer
        sessionId={sessionId}
        model={chosenModel}
        disabled={!running || noModel}
        disabledReason={
          noModel
            ? "Add a model under Settings, Models to send a message."
            : "The workspace is not running; start it to send a message."
        }
      />
    </section>
  );
}
