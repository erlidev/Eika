/**
 * The commands the composer understands in place of a message. There is one:
 * `/compact`, which summarizes the older part of the conversation now, as
 * Pi's command of the same name does. What follows it says what the summary
 * should focus on.
 */

/**
 * compactCommand reads `/compact [focus]` and returns the focus, empty for
 * none, or null for text that is a message rather than the command.
 */
export function compactCommand(text: string): string | null {
  const match = /^\/compact(?:\s+([\s\S]*))?$/.exec(text.trim());
  if (match === null) return null;
  return (match[1] ?? "").trim();
}
