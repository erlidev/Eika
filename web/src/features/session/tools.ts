/** Tools as the UI describes them: the one line each tool is described by. */

/**
 * toolSummary is the first sentence of a tool's description. The description
 * is written for the model and runs to several lines; a person choosing the
 * tool needs the one that says what it is.
 */
export function toolSummary(description: string): string {
  const line = description.split("\n", 1)[0]?.trim() ?? "";
  const end = line.search(/\.(\s|$)/);
  return end === -1 ? line : line.slice(0, end + 1);
}
