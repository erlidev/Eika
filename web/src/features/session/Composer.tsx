/**
 * The message box. It is one control in three modes: it starts a run when the
 * session is idle, and while a run is going the same text can be steered into
 * the run or queued as a follow-up.
 *
 * The buttons and the key hints sit inside the box rather than under it. The
 * box is the widest thing in the pane and its bottom edge was empty; putting
 * them there gives the transcript back the row they used to cost.
 */

import { useRef, useState } from "react";
import { ArrowUp } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { usePostMessage, useRunStatus } from "@/features/session/queries";
import type { MessageMode } from "@/api/types";
import { failureText } from "@/lib/failure";

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
  const empty = text.trim() === "";

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
      <div className="mx-auto w-full max-w-3xl px-3 pt-3 pb-1.5">
        {/*
          The wrapper carries the border and the focus ring so that the box and
          the row of controls under it read as one field. The textarea inside
          it is bare: two borders around one control look like two controls.
        */}
        <div className="border-input focus-within:border-ring focus-within:ring-ring/50 rounded-md border transition-colors focus-within:ring-3">
          <Textarea
            ref={box}
            value={text}
            disabled={disabled}
            aria-label="Message"
            placeholder={
              active ? "Steer the run, or queue a follow-up…" : "Send a message to the agent…"
            }
            rows={2}
            // field-sizing grows the box with the text, which without a cap
            // would push the transcript out of the pane on a long message.
            className="max-h-64 min-h-0 resize-none border-0 bg-transparent px-2.5 py-2 text-sm focus-visible:border-0 focus-visible:ring-0 dark:bg-transparent"
            onChange={(e) => {
              setText(e.target.value);
            }}
            onKeyDown={(e) => {
              // An input method ends its composition with Enter. Sending on it
              // would swallow the word the user was still typing.
              if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing) return;
              e.preventDefault();
              send(active ? "steer" : "run");
            }}
          />
          <div className="flex items-center gap-2 px-2 pb-1.5">
            <p className="text-muted-foreground min-w-0 truncate text-2xs">
              {disabled ? disabledReason : "Enter sends · Shift+Enter newline · Esc aborts"}
            </p>
            <span className="ml-auto flex shrink-0 items-center gap-1.5">
              {active && (
                <Button
                  size="xs"
                  variant="secondary"
                  disabled={disabled || empty || post.isPending}
                  onClick={() => {
                    send("follow_up");
                  }}
                >
                  Follow-up
                </Button>
              )}
              <Button
                size="xs"
                disabled={disabled || empty || post.isPending}
                onClick={() => {
                  send(active ? "steer" : "run");
                }}
              >
                {active ? "Steer" : "Send"}
                <ArrowUp aria-hidden className="size-3" />
              </Button>
            </span>
          </div>
        </div>
        {post.isError && (
          <p role="alert" className="text-destructive mt-2 text-xs">
            {failureText("send the message", post.error)}
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
          <li key={`${String(index)}:${message}`} className="truncate">
            {message}
          </li>
        ))}
      </ol>
    </div>
  );
}
