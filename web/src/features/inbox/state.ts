/** The pure part of the Inbox: what an answer's failure says. */

import { ApiError } from "@/api/client";
import type { TripwireEntry, TripwireName } from "@/api/types";
import { failureText } from "@/lib/failure";

/** Answer is what the user did to a pending memory. */
export type Answer = "accept" | "reject";

/**
 * answerFailure says plainly why an answer did not go through. A stale or
 * repeated answer (409) was refused and nothing changed, a credential (400)
 * names itself in the harness's reason, and 503 is memory or the injection
 * screen not being ready: the memory stays pending in every case.
 */
export function answerFailure(answer: Answer, error: unknown): string {
  const verb = answer === "accept" ? "accept" : "reject";
  if (error instanceof ApiError) {
    if (error.status === 409) {
      return "This memory changed or was already answered, possibly on another device, so nothing was saved. The card now shows its current state.";
    }
    if (error.status === 400) return `Not ${verb}ed: ${error.message}`;
    if (error.status === 503) {
      return "Memory is not ready, or the injection screen it requires is not answering. The memory stays pending; try again in a moment.";
    }
  }
  return failureText(`${verb} the memory`, error);
}

/** tripwireTitle names the chain in the words of the design, for a card's heading. */
export const tripwireTitle: Record<TripwireName, string> = {
  followed: "A suspect page's destination was followed",
  carried_out: "Private text was sent to a new place",
  lifted: "A sensitive read, then a new connection",
  drained: "A large amount of data was sent out",
  blocked_often: "Blocked many times in one run",
};

/** TripwireAnswer is what the user did to an open entry. */
export type TripwireAnswer = "fine" | "stop";

/**
 * tripwireState says what the entry's run is doing now, so the card is honest
 * about what the answers can still change: a hold lasts only while its run does.
 */
export function tripwireState(entry: TripwireEntry): string {
  if (!entry.running) {
    return entry.held ? "The run has ended, and with it the hold." : "The run has ended.";
  }
  return entry.held
    ? "The run is held: its fetches and connections to places it has not been given are blocked."
    : "The run carries on.";
}

/**
 * tripwireFailure says plainly why an answer did not go through. 409 means the
 * entry was answered elsewhere, or "stop" came after the run ended.
 */
export function tripwireFailure(answer: TripwireAnswer, error: unknown): string {
  if (error instanceof ApiError && error.status === 409) {
    return answer === "stop"
      ? "The run already ended, or this was answered on another device, so nothing was stopped."
      : "This was already answered, possibly on another device. The card now shows its current state.";
  }
  return failureText(answer === "stop" ? "stop the run" : "answer this", error);
}
