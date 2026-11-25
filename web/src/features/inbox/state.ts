/** The pure part of the Inbox: what an answer's failure says. */

import { ApiError } from "@/api/client";
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
