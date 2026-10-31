/**
 * The centre pane: one session's transcript, the composer, and the run status
 * bar under it. It owns the session's stream subscription.
 */

import { House, MessageCircle } from "lucide-react";
import { useEffect } from "react";

import { Composer } from "@/features/session/Composer";
import { abortsRun } from "@/features/session/escape";
import { useAbortRun, useRunStatus, useSession } from "@/features/session/queries";
import { RunStatusBar } from "@/features/session/RunStatusBar";
import { SessionHeading } from "@/features/session/SessionHeading";
import { Transcript } from "@/features/session/Transcript";
import { useSessionStore } from "@/features/session/store";
import { useSessionStream } from "@/features/session/useSessionStream";
import { Notice } from "@/components/Notice";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useSessionConfiguration } from "@/features/profiles";
import { useModels } from "@/features/providers";
import { useSettingsDialog } from "@/features/settings";
import { useHomeComputer } from "@/features/sessions";

export type SessionViewProps = {
  sessionId: string;
};

export function SessionView({ sessionId }: SessionViewProps) {
  useSessionStream(sessionId);
  const session = useSession(sessionId);
  const models = useModels();
  const configuration = useSessionConfiguration(sessionId);
  const showSettings = useSettingsDialog((s) => s.show);
  const status = useRunStatus(sessionId);
  const abort = useAbortRun(sessionId);
  const model = useSessionStore((s) => s.model);
  const chooseModel = useSessionStore((s) => s.chooseModel);

  const home = useHomeComputer();
  const computerId = session.data?.session.computer_id;
  const chat = session.data !== undefined && computerId === undefined;
  const onHome = computerId !== undefined && computerId === home.data?.id;

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

  // A model chosen in the status bar is this browser's; without one, a run
  // uses what the session's configuration resolves to.
  const chosenModel =
    model === "" ? (configuration.data?.resolved.model ?? models.data?.default ?? "") : model;
  const noModel = models.data?.models.length === 0;
  const imageInput = models.data?.models.find((m) => m.name === chosenModel)?.image_input ?? false;

  return (
    <section className="flex min-h-0 flex-1 flex-col" aria-label="Session">
      <header className="flex items-center gap-2 border-b px-3 py-2">
        <SessionHeading session={session.data?.session} />
        {chat && <ChatBadge />}
        {onHome && <HomeBadge />}
        {/* On a phone the id gives way: the title is what the header is for. */}
        <span className="text-muted-foreground hidden shrink-0 font-mono text-xs sm:inline">
          {sessionId}
        </span>
      </header>
      {/* The transcript owns its scroller: it has to measure it to decide
          whether the user is still following the stream. */}
      <Transcript
        sessionId={sessionId}
        empty={
          chat
            ? "Send a message to start the chat. A chat has no computer, so the model cannot touch files or run commands; the Tools panel says what it can use."
            : onHome
              ? "Send a message to start the chat. It runs on the home computer, so the model can run commands and keep files in /home/eika."
              : "Send a message to start the first turn."
        }
      />
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
      <Composer
        sessionId={sessionId}
        model={chosenModel}
        imageInput={imageInput}
        {...(chat ? { placeholder: "Send a message…" } : {})}
        disabled={noModel}
        disabledReason="Add a model under Settings, Models to send a message."
      />
      {/* The status bar is the pane's footer: the model, the meter, and the
          connection are what the session is running on, not what it is
          composing, so they read under the box rather than over it. */}
      <RunStatusBar
        sessionId={sessionId}
        chat={chat}
        overridden={session.data?.session.overridden ?? false}
        model={chosenModel}
        onModelChange={chooseModel}
      />
    </section>
  );
}

/**
 * ChatBadge marks a session with no computer. It is the first thing beside
 * the title, so a chat is never taken for a session whose agent can touch
 * files.
 */
function ChatBadge() {
  return (
    <Badge variant="outline" className="h-5 shrink-0 gap-1 px-1.5 text-2xs">
      <MessageCircle aria-hidden className="size-3" />
      Chat
      <span className="text-muted-foreground font-normal">· no computer</span>
    </Badge>
  );
}

/**
 * HomeBadge marks a session on the home computer, the one place a chat's
 * model has files and commands.
 */
function HomeBadge() {
  return (
    <Badge variant="outline" className="h-5 shrink-0 gap-1 px-1.5 text-2xs">
      <House aria-hidden className="size-3" />
      Home computer
    </Badge>
  );
}
