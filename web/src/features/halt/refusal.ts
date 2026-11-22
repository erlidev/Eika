/** How a refused message reads while Eika is halted. */

import { ApiError } from "@/api/client";
import { failureText } from "@/lib/failure";

/** isHaltRefusal reports whether a 409 is the harness refusing work because Eika is halted. */
export function isHaltRefusal(error: unknown): boolean {
  return (
    error instanceof ApiError && error.status === 409 && error.message.startsWith("Eika is halted")
  );
}

/**
 * sendFailureText words a failed send: a halted Eika says so and where to
 * resume, keeping the draft; anything else is the usual failure.
 */
export function sendFailureText(action: string, error: unknown): string {
  if (isHaltRefusal(error))
    return `Eika is halted, so nothing was sent. Your text is kept: press Resume in the banner, then send it again.`;
  return failureText(action, error);
}
