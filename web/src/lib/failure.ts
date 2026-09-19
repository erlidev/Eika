/**
 * How the UI words a failed action: what the user asked for, then what
 * actually went wrong, so an error never reads as a bare cause with no
 * context ("name taken") or as the wrong cause.
 */

/**
 * Words that start a sentence the harness or the client wrote, lowered when
 * the sentence is joined after a colon. Anything else, such as a provider's
 * name, keeps its capital.
 */
const sentenceStarts = new Set(["The", "That", "This", "A", "An", "No", "It", "There", "Enter"]);

/** causeOf is an error's message as a clause, or a fallback when it has none. */
function causeOf(error: unknown): string {
  const message = error instanceof Error ? error.message.trim() : "";
  if (message === "") return "the harness gave no reason. Try again";
  const [first = "", ...rest] = message.split(" ");
  const clause = sentenceStarts.has(first) ? [first.toLowerCase(), ...rest].join(" ") : message;
  return clause;
}

/**
 * failureText says that an action failed and why, as one sentence or more:
 * `failureText("reopen the setup", err)` is "Could not reopen the setup: the
 * harness could not be reached. Check that it is running, then try again."
 * The action is a verb phrase in the imperative, lowercase.
 */
export function failureText(action: string, error: unknown): string {
  const cause = causeOf(error);
  const ended = /[.!?]$/.test(cause) ? cause : `${cause}.`;
  return `Could not ${action}: ${ended}`;
}
