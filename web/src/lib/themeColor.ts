/**
 * The design tokens of `index.css` as plain colours, for the two widgets that
 * draw their own pixels (the terminal and the code editor) and cannot take a
 * CSS variable. Read them again when the theme changes.
 */

let canvas: CanvasRenderingContext2D | null | undefined;

/**
 * themeColor reads a token such as "background" as `#rrggbb`. The browser
 * resolves whatever colour syntax the token uses, so a token keeps being the
 * only place a colour is written.
 */
export function themeColor(token: string): string {
  const value = getComputedStyle(document.documentElement).getPropertyValue(`--${token}`).trim();
  return toHex(value);
}

function toHex(css: string): string {
  canvas ??= document.createElement("canvas").getContext("2d", { willReadFrequently: true });
  if (!canvas || css === "") return "#000000";
  canvas.clearRect(0, 0, 1, 1);
  canvas.fillStyle = "#000000";
  canvas.fillStyle = css;
  canvas.fillRect(0, 0, 1, 1);
  const [r = 0, g = 0, b = 0] = canvas.getImageData(0, 0, 1, 1).data;
  return `#${[r, g, b].map((n) => n.toString(16).padStart(2, "0")).join("")}`;
}
