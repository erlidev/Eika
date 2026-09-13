/**
 * The message box. It is one control in three modes: it starts a run when the
 * session is idle, and while a run is going the same text can be steered into
 * the run or queued as a follow-up.
 */

import { useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { usePostMessage, useRunStatus } from "@/features/session/queries";
import type { MessageMode } from "@/api/types";
import { cn } from "@/lib/utils";

export type ComposerProps = {
  sessionId: string;
  /** model is the model a new run uses; empty means the deployment default. */
  model: string;
  /** disabled stops the composer when the workspace is not running. */
  disabled?: boolean;
  /** disabledReason explains why, under the box. */
  disabledReason?: string;
};

export function Composer({ sessionId, model, disabled = false, disabledReason }: ComposerProps) {
  const [text, setText] = useState("");
  const box = useRef<HTMLTextAreaElement>(null);
  const status = useRunStatus(sessionId);
  const post = usePostMessage(sessionId);
  const active = status.data?.active ?? false;

  const send = (mode: MessageMode) => {
    const trimmed = text.trim();
    if (trimmed === "" || disabled) return;
    post.mutate(
      { text: trimmed, mode, ...(model === "" ? {} : { model }) },
      {
        onSuccess: () => {
          setText("");
        },
      },
    );
  };

  return (
    <div className="bg-background border-t">
      <div className="mx-auto w-full max-w-3xl p-3">
        <Textarea
          ref={box}
          value={text}
          disabled={disabled}
          aria-label="Message"
          placeholder={
            active ? "Steer the run, or queue a follow-up…" : "Send a message to the agent…"
          }
          rows={3}
          className={cn("resize-none font-mono text-sm")}
          onChange={(e) => {
            setText(e.target.value);
          }}
          onKeyDown={(e) => {
            if (e.key !== "Enter" || e.shiftKey) return;
            e.preventDefault();
            send(active ? "steer" : "run");
          }}
        />
        <div className="mt-2 flex flex-wrap items-center gap-2">
          {active ? (
            <>
              <Button
                size="sm"
                disabled={disabled || post.isPending}
                onClick={() => {
                  send("steer");
                }}
              >
                Steer
              </Button>
              <Button
                size="sm"
                variant="secondary"
                disabled={disabled || post.isPending}
                onClick={() => {
                  send("follow_up");
                }}
              >
                Follow-up
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              disabled={disabled || post.isPending}
              onClick={() => {
                send("run");
              }}
            >
              Send
            </Button>
          )}
          <p className="text-muted-foreground ml-auto text-xs">
            {disabled ? disabledReason : "Enter sends · Shift+Enter newline · Esc aborts"}
          </p>
        </div>
        {post.isError && (
          <p role="alert" className="text-destructive mt-2 text-xs">
            {post.error.message}
          </p>
        )}
        <Queues
          steering={status.data?.pending_steering ?? []}
          followUps={status.data?.pending_follow_ups ?? []}
        />
      </div>
    </div>
  );
}

function Queues({ steering, followUps }: { steering: string[]; followUps: string[] }) {
  if (steering.length === 0 && followUps.length === 0) return null;
  return (
    <div className="text-muted-foreground mt-2 space-y-1 text-xs">
      <Queue label="Steering" messages={steering} />
      <Queue label="Follow-up" messages={followUps} />
      <p className="italic">Queued messages cannot be removed; abort the run to discard them.</p>
    </div>
  );
}

function Queue({ label, messages }: { label: string; messages: string[] }) {
  if (messages.length === 0) return null;
  return (
    <div>
      <span className="font-medium">{label}</span>
      <ol className="mt-0.5 space-y-0.5">
        {messages.map((message, index) => (
          <li key={`${String(index)}:${message}`} className="truncate font-mono">
            {message}
          </li>
        ))}
      </ol>
    </div>
  );
}
