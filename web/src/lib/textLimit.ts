/** The limit the harness puts on each text the user writes: instructions and agent prompts. */

/** maxTextBytes is the most UTF-8 bytes of one text the harness accepts. */
export const maxTextBytes = 65536;

/** textBytes is the UTF-8 size of a text, which is what the harness limits. */
export function textBytes(text: string): number {
  return new TextEncoder().encode(text).length;
}

/**
 * textProblem says why the harness would refuse a text, or "" when it would
 * take it: it counts bytes, not characters, and refuses a NUL.
 */
export function textProblem(text: string): string {
  if (text.includes("\0")) return "The text must not contain a NUL character.";
  const bytes = textBytes(text);
  if (bytes > maxTextBytes) {
    return `The text is ${bytes.toLocaleString("en-US")} bytes; the limit is ${maxTextBytes.toLocaleString("en-US")}.`;
  }
  return "";
}
