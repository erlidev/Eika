/**
 * Keyboard movement within a tablist. The WAI-ARIA tabs pattern makes the
 * list one tab stop: Tab reaches the selected tab, and the arrows move
 * between them.
 */

/**
 * nextTabIndex is where an arrow key moves the focus within a tablist, or
 * null for a key the list does not handle. The ends wrap, which is what the
 * pattern specifies and what makes a two-tab list quick to flip between.
 */
export function nextTabIndex(from: number, count: number, key: string): number | null {
  if (count === 0) return null;
  switch (key) {
    case "ArrowLeft":
      return (from - 1 + count) % count;
    case "ArrowRight":
      return (from + 1) % count;
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return null;
  }
}
