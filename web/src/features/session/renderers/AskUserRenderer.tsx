/**
 * The `ask_user` card. While the run waits, the card is the answer form; once
 * the call has finished it shows the answer the model received.
 */

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAnswerQuestion } from "@/features/session/queries";
import { stringArg } from "@/features/session/renderers/registry";
import type { ToolRendererProps } from "@/features/session/renderers/registry";
import { useSessionStore } from "@/features/session/store";

/** AskUserBody is the renderer body registered for the `ask_user` tool. */
export function AskUserBody({ call }: ToolRendererProps) {
  const sessionId = useSessionStore((s) => s.sessionId);
  const question = useSessionStore((s) => s.questions.find((q) => q.call_id === call.callId));
  const dismissQuestion = useSessionStore((s) => s.dismissQuestion);
  const answer = useAnswerQuestion(sessionId);
  const [freeText, setFreeText] = useState("");

  const text = question?.question ?? stringArg(call, "question");
  const options = question?.options ?? [];
  const allowFreeText = question?.allow_free_text ?? options.length === 0;

  if (!question) {
    return (
      <div className="space-y-2">
        <p className="text-sm">{text}</p>
        <p className="text-muted-foreground font-mono text-xs">
          {call.done ? `answered: ${call.content ?? ""}` : "waiting for the harness…"}
        </p>
      </div>
    );
  }

  const send = (value: string) => {
    const trimmed = value.trim();
    if (trimmed === "") return;
    // The card disappears as soon as the answer is accepted; the run
    // continues and its tool.result clears the question for good.
    answer.mutate(
      { id: question.id, answer: trimmed },
      {
        onSuccess: () => {
          dismissQuestion(question.id);
        },
      },
    );
  };

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        send(freeText);
      }}
    >
      <p className="text-sm font-medium">{text}</p>
      {options.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {options.map((option) => (
            <Button
              key={option}
              type="button"
              size="sm"
              variant="secondary"
              disabled={answer.isPending}
              onClick={() => {
                send(option);
              }}
            >
              {option}
            </Button>
          ))}
        </div>
      )}
      {allowFreeText && (
        <div className="space-y-1">
          <Label htmlFor={`answer-${question.id}`} className="text-muted-foreground text-xs">
            Your answer
          </Label>
          <div className="flex gap-2">
            <Input
              id={`answer-${question.id}`}
              value={freeText}
              autoComplete="off"
              onChange={(e) => {
                setFreeText(e.target.value);
              }}
              placeholder="Type an answer"
              className="h-8"
            />
            <Button type="submit" size="sm" disabled={answer.isPending || freeText.trim() === ""}>
              Send
            </Button>
          </div>
        </div>
      )}
      {answer.isError && (
        <p className="text-destructive text-xs" role="alert">
          {answer.error.message}
        </p>
      )}
    </form>
  );
}
