import type { Entry } from "@/api/types";

export type EntryPage = { entries: Entry[]; next_before?: string };

/** readCatchUp reads every page back to the newest entry already held. */
export async function readCatchUp(
  read: (before?: string) => Promise<EntryPage>,
  newest: string | undefined,
  newestOnly: boolean,
): Promise<{ entries: Entry[]; nextBefore?: string }> {
  const entries: Entry[] = [];
  let before: string | undefined;
  let nextBefore: string | undefined;
  let first = true;
  do {
    const page = await read(before);
    entries.push(...page.entries);
    if (first) nextBefore = page.next_before;
    first = false;
    if (newestOnly || (newest !== undefined && page.entries.some((entry) => entry.id === newest)))
      break;
    before = page.next_before;
  } while (before !== undefined);
  return { entries, ...(nextBefore === undefined ? {} : { nextBefore }) };
}
