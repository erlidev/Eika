/** Flagged text split for marking: screen spans are UTF-8 byte offsets into a field. */

/** ByteSpan is the part of a screen span that marking needs: a field and UTF-8 byte offsets. */
export type ByteSpan = { field: string; start: number; end: number };

/** Segment is a run of a field's text, flagged or not. */
export type Segment = { text: string; flagged: boolean };

/**
 * segments splits a field's text at the screen's spans of that field. The
 * harness counts spans in bytes of the UTF-8 text, so the split works on
 * bytes; overlapping spans merge and a span outside the text is dropped.
 */
export function segments(text: string, field: string, spans: readonly ByteSpan[]): Segment[] {
  const bytes = new TextEncoder().encode(text);
  const decoder = new TextDecoder();
  const ranges = spans
    .filter((s) => s.field === field && s.start >= 0 && s.start < s.end && s.start < bytes.length)
    .map((s) => ({ start: s.start, end: Math.min(s.end, bytes.length) }))
    .sort((a, b) => a.start - b.start);
  const out: Segment[] = [];
  let at = 0;
  for (const range of ranges) {
    const start = Math.max(range.start, at);
    if (range.end <= start) continue;
    if (start > at) out.push({ text: decoder.decode(bytes.slice(at, start)), flagged: false });
    const text = decoder.decode(bytes.slice(start, range.end));
    const last = out[out.length - 1];
    if (last?.flagged === true && start === at) last.text += text;
    else out.push({ text, flagged: true });
    at = range.end;
  }
  if (at < bytes.length) out.push({ text: decoder.decode(bytes.slice(at)), flagged: false });
  return out.length === 0 ? [{ text, flagged: false }] : out;
}
