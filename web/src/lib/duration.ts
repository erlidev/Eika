/**
 * formatDuration renders a millisecond duration compactly, the way tool call
 * and turn timings are shown in the UI.
 */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) {
    return "-";
  }
  if (ms < 1000) {
    return `${String(Math.round(ms))}ms`;
  }
  const seconds = ms / 1000;
  if (seconds < 60) {
    return `${seconds.toFixed(1)}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const rest = Math.round(seconds - minutes * 60);
  return `${String(minutes)}m ${String(rest)}s`;
}
